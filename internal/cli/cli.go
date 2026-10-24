// Package cli 实现 `archiver>` 交互式命令行。
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"nico-seiga-archiver/internal/archiver"
	"nico-seiga-archiver/internal/config"
	"nico-seiga-archiver/internal/getuploader"
	"nico-seiga-archiver/internal/logx"
	"nico-seiga-archiver/internal/nico"
	"nico-seiga-archiver/internal/prompt"
	"nico-seiga-archiver/internal/proxy"
	"nico-seiga-archiver/internal/util"
)

// version 由 Makefile 通过 -ldflags "-X .../internal/cli.version=<git commit>" 注入；
// 直接用 `go run` / `go build` 构建时为 dev。
var version = "dev"

const banner = `
  ┌──────────────────────────────────────────────────────────┐
  │  nico-seiga-archiver  ·  getuploader 立绘抢救工具         │
  └──────────────────────────────────────────────────────────┘
`

// CLI 是交互式命令行状态。
type CLI struct {
	cfg         *config.Config
	cfgPath     string
	historyPath string
	log         *logx.Logger
	arch        *archiver.Archiver
	pr          *prompt.Reader
	out         io.Writer
	// cliMode 表示本次是非交互的命令行调用（用于决定 help 输出哪种用法）。
	cliMode bool
}

// New 创建 CLI。
func New() (*CLI, error) {
	cfg, path, err := config.Load()
	if err != nil {
		// 配置坏了也让用户进来修。
		fmt.Fprintf(os.Stderr, "读取配置失败: %v\n", err)
		cfg = config.Default()
	}
	cfg.Normalize()
	level := parseLevel(cfg.LogLevel)
	logger := logx.New(os.Stdout, level)
	pr := prompt.New(os.Stdin, os.Stdout)
	c := &CLI{
		cfg:     cfg,
		cfgPath: path,
		log:     logger,
		pr:      pr,
		out:     os.Stdout,
	}
	if hp, err := config.HistoryPath(); err == nil {
		c.historyPath = hp
		_ = pr.LoadHistory(hp)
	}
	pr.SetCompleter(completeCommand)
	return c, nil
}

// attachLogFile 按配置打开日志文件；失败只警告，不影响主流程。
func (c *CLI) attachLogFile() {
	path := c.cfg.LogPath()
	if path == "" {
		c.log.DetachFile()
		return
	}
	if err := c.log.AttachFile(path); err != nil {
		c.log.Warnf("日志文件不可用（%v），将只输出到终端", err)
	}
}

// Run 启动 REPL；args 非空时以非交互方式执行一条命令后退出。
func (c *CLI) Run(args []string) int {
	defer func() {
		c.saveHistory()
		c.pr.Close()
		c.log.Close()
	}()

	if len(args) > 0 {
		return c.runOnce(args)
	}

	c.printBanner()

	for {
		line, err := c.pr.Line("archiver> ")
		if err != nil {
			switch {
			case errors.Is(err, prompt.ErrInterrupted):
				// 顶层没有「上一级」，Ctrl+C 就是退出。
				fmt.Fprintln(c.out)
				c.log.Infof("已退出。")
				return 0
			case errors.Is(err, io.EOF):
				fmt.Fprintln(c.out)
				return 0
			default:
				c.log.Errorf("读取输入失败: %v", err)
				return 1
			}
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !isSecretCommand(line) {
			c.pr.AddHistory(line)
		}
		rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		exit, err := c.execute(rootCtx, line)
		stop()
		switch {
		case err == nil:
		case errors.Is(err, prompt.ErrInterrupted):
			c.log.Warnf("已取消当前操作，返回命令提示符。")
		case isCancelled(err):
			c.log.Warnf("已取消当前任务，返回命令提示符。")
		default:
			c.log.Errorf("%v", err)
		}
		if exit {
			c.log.Infof("已退出。")
			return 0
		}
	}
}

// runOnce 以非交互方式执行一条命令，返回进程退出码。
//
// 不打印 banner，方便脚本/管道使用。
func (c *CLI) runOnce(argv []string) int {
	c.cliMode = true
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	_, err := c.executeArgs(ctx, argv)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, prompt.ErrInterrupted):
		fmt.Fprintln(c.out)
		c.log.Warnf("已取消。")
		return 130
	case isCancelled(err):
		c.log.Warnf("已取消。")
		return 130
	default:
		c.log.Errorf("%v", err)
		return exitCodeFor(err)
	}
}

// exitCodeFor 把错误映射成退出码。
func exitCodeFor(err error) int {
	var ue usageError
	if errors.As(err, &ue) {
		return 2
	}
	return 1
}

// usageError 表示参数用法错误（退出码 2）。
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return usageError{msg: fmt.Sprintf(format, args...)}
}

// promptLine 读取一行输入（历史 + 行内编辑）。
func (c *CLI) promptLine(p string) (string, error) {
	return c.pr.Line(p)
}

// promptPassword 读取密码（不回显）。
func (c *CLI) promptPassword(p string) (string, error) {
	return c.pr.Password(p)
}

func (c *CLI) saveHistory() {
	if c.historyPath == "" {
		return
	}
	if err := c.pr.SaveHistory(c.historyPath); err != nil {
		c.log.Debugf("保存命令历史失败: %v", err)
	}
}

// isCancelled 判断错误是否来自 Ctrl+C 触发的 context 取消。
func isCancelled(err error) bool {
	return errors.Is(err, context.Canceled)
}

// isSecretCommand 判断命令是否包含敏感信息（不写入命令历史）。
func isSecretCommand(line string) bool {
	fields := util.SplitArgs(line)
	if len(fields) < 3 {
		return false
	}
	if !strings.EqualFold(fields[0], "cfg") && !strings.EqualFold(fields[0], "config") {
		return false
	}
	if !strings.EqualFold(fields[1], "set") {
		return false
	}
	switch strings.ToLower(fields[2]) {
	case "cookie", "nico_cookie", "nicocookie", "user_agent", "ua":
		return true
	}
	return false
}

// completeCommand 提供命令名的 Tab 补全。
func completeCommand(line string) []string {
	fields := strings.Fields(line)
	if len(fields) > 1 {
		return nil
	}
	prefix := ""
	if len(fields) == 1 {
		prefix = fields[0]
		if strings.HasSuffix(line, " ") {
			return nil
		}
	}
	var out []string
	for _, cmd := range commandNames {
		if strings.HasPrefix(cmd, prefix) {
			out = append(out, cmd)
		}
	}
	return out
}

var commandNames = []string{
	"help", "version", "exit", "quit", "clear",
	"nico-search", "nico-tag", "nico-author", "nico-down",
	"down", "down-catalog",
	"cfg", "progress", "probe", "stats",
}

func (c *CLI) printBanner() {
	fmt.Fprint(c.out, banner)
	fmt.Fprintf(c.out, "  版本 %s\n", version)
	fmt.Fprintf(c.out, "  配置文件  : %s\n", orDash(c.cfgPath))
	fmt.Fprintf(c.out, "  下载目录  : %s\n", c.cfg.DownloadDir)
	fmt.Fprintf(c.out, "  元数据    : %s\n", c.cfg.MetadataPath())
	if lp := c.cfg.LogPath(); lp != "" {
		fmt.Fprintf(c.out, "  日志      : %s\n", lp)
	}
	pr := proxy.Resolve(c.cfg.Proxy)
	if pr.URL == "" {
		fmt.Fprintf(c.out, "  代理      : 直连 (%s)\n", pr.Source)
	} else {
		fmt.Fprintf(c.out, "  代理      : %s (%s)\n", pr.URL, pr.Source)
	}
	if strings.TrimSpace(c.cfg.NicoCookie) == "" {
		fmt.Fprintf(c.out, "  提示      : 未设置 nico Cookie（可选）。遇到仅登录可见的投稿时，输入 cfg 设置。\n")
	}
	if c.pr.Interactive() {
		fmt.Fprintln(c.out, "  ↑/↓ 翻命令历史 · Tab 补全（帮助见下方，快捷键见 help keys）")
	}
	// 启动时直接展示帮助，省去用户先输入 help。
	c.showHelp(nil)
}

func (c *CLI) execute(ctx context.Context, line string) (exit bool, err error) {
	args := util.SplitArgs(line)
	if len(args) == 0 {
		return false, nil
	}
	return c.executeArgs(ctx, args)
}

// executeArgs 执行一条已切分好的命令（REPL 与命令行模式共用）。
func (c *CLI) executeArgs(ctx context.Context, argv []string) (exit bool, err error) {
	argv = inferCommand(argv)
	opts, err := parseOptions(argv)
	if err != nil {
		return false, usageError{msg: err.Error()}
	}
	args := opts.argv
	if len(args) == 0 {
		return false, usagef("缺少命令，输入 archiver --help 查看用法")
	}

	// 只有 help/version 时才先应用参数（保证 --dir 等值能显示在帮助里）。
	cmd := strings.ToLower(args[0])
	rest := args[1:]
	if cmd == "help" || cmd == "?" || cmd == "h" {
		opts.apply(c)
		if c.cliMode {
			c.printUsage()
		} else {
			c.showHelp(rest)
		}
		return false, nil
	}
	if cmd == "version" {
		fmt.Fprintf(c.out, "nico-seiga-archiver %s\n", version)
		return false, nil
	}

	opts.apply(c)

	switch cmd {
	case "exit", "quit", "q":
		return true, nil
	case "clear", "cls":
		fmt.Fprint(c.out, "\x1b[H\x1b[2J")
		return false, nil
	}

	// 从这里开始会真正执行任务，此时才启用日志文件，
	// 避免 --version / --help / exit 这类操作也生成一个空日志。
	c.attachLogFile()

	if cmd == "cfg" || cmd == "config" {
		return false, c.cfgCommand(ctx, rest)
	}

	arch, err := c.ensureArchiver()
	if err != nil {
		return false, err
	}
	arch.Force = opts.force
	arch.DryRun = opts.dryRun
	arch.Restart = opts.restart
	arch.ResetStats()

	switch cmd {
	case "nico-search", "search", "ns":
		return false, c.cmdNicoSearch(ctx, arch, rest)
	case "nico-author", "author", "na":
		return false, c.cmdNicoAuthor(ctx, arch, rest)
	case "nico-tag", "tag", "nt":
		return false, c.cmdNicoTag(ctx, arch, rest)
	case "nico-down", "nico", "nd":
		return false, c.cmdNicoDown(ctx, arch, rest)
	case "down", "d":
		return false, c.cmdDown(ctx, arch, rest)
	case "down-catalog", "catalog", "dc":
		return false, c.cmdDownCatalog(ctx, arch, rest)
	case "probe", "test":
		return false, c.cmdProbe(ctx, arch)
	case "stats":
		fmt.Fprintf(c.out, "%s\n", arch.Stats().String())
		return false, nil
	case "progress":
		return false, c.cmdProgress(arch, rest)
	default:
		return false, usageError{msg: hintForUnknown(args[0])}
	}
}

func (c *CLI) ensureArchiver() (*archiver.Archiver, error) {
	if c.arch != nil {
		return c.arch, nil
	}
	a, err := archiver.New(c.cfg, c.log)
	if err != nil {
		return nil, fmt.Errorf("初始化失败: %w", err)
	}
	a.AskPassword = c.askPassword
	c.arch = a
	c.log.Debugf("代理来源: %s (%s)", a.ProxyInfo.URL, a.ProxyInfo.Source)
	return a, nil
}

func (c *CLI) rebuild() {
	c.arch = nil
	c.log.SetLevel(parseLevel(c.cfg.LogLevel))
}

// ---------- 命令实现 ----------

func (c *CLI) cmdNicoSearch(ctx context.Context, a *archiver.Archiver, args []string) error {
	if len(args) < 1 {
		return usagef("用法: nico-search <搜索页URL|关键词> [起始页]")
	}
	input := strings.TrimSpace(args[0])
	var listURL string
	if strings.Contains(input, "://") || strings.Contains(input, "seiga.nicovideo.jp") {
		listURL = input
		if !strings.Contains(listURL, "://") {
			listURL = "https://" + listURL
		}
	} else {
		listURL = nico.SearchURLFromKeyword(input)
		c.log.Infof("用关键词「%s」构造搜索地址: %s", input, listURL)
	}
	page, err := optionalPage(args, 1)
	if err != nil {
		return err
	}
	key, label := searchKeyAndLabel(input, listURL)
	return c.runList(ctx, a, listURL, page, key, label)
}

func (c *CLI) cmdNicoAuthor(ctx context.Context, a *archiver.Archiver, args []string) error {
	if len(args) < 1 {
		return usagef("用法: nico-author <作者主页URL> [起始页]")
	}
	pageURL, id, err := nico.NormalizeUserURL(args[0])
	if err != nil {
		return err
	}
	page, err := optionalPage(args, 1)
	if err != nil {
		return err
	}
	return c.runList(ctx, a, pageURL, page, nico.AuthorProgressKey(id), "作者 "+id)
}

func (c *CLI) cmdNicoTag(ctx context.Context, a *archiver.Archiver, args []string) error {
	if len(args) < 1 {
		return usagef("用法: nico-tag <tag|tag页面URL> [起始页]")
	}
	listURL, tag, err := nico.NormalizeTagURL(args[0])
	if err != nil {
		return err
	}
	page, err := optionalPage(args, 1)
	if err != nil {
		return err
	}
	return c.runList(ctx, a, listURL, page, nico.TagProgressKey(tag), "tag「"+tag+"」")
}

// optionalPage 解析可选的页码参数；未给出时返回 def（后续会与历史进度比较）。
func optionalPage(args []string, def int) (int, error) {
	if len(args) < 2 {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(args[1]))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("起始页必须是正整数: %s", args[1])
	}
	return n, nil
}

// runList 统一处理「显式页码 / 历史进度」并执行翻页下载。
func (c *CLI) runList(ctx context.Context, a *archiver.Archiver, listURL string, page int, key, label string) error {
	explicit := page > 1
	if !explicit {
		switch {
		case a.Restart:
			c.log.Infof("已忽略历史进度（--restart），从第 1 页开始")
		default:
			if e, ok := a.ResumePage(key); ok && e.LastPage > 0 {
				note := ""
				if e.Completed {
					note = "，上次已翻到最后一页"
				}
				c.log.Infof("检测到历史进度 %s：上次停在第 %d 页%s，从该页继续（--restart 可从头开始）", key, e.LastPage, note)
				page = e.LastPage
			}
		}
	}
	c.log.Stepf("开始%s索引: %s（从第 %d 页开始）", label, listURL, page)
	return a.ProcessList(ctx, listURL, page, key, label)
}

// searchKeyAndLabel 从关键词或搜索 URL 中提取进度用的键与可读标签。
func searchKeyAndLabel(input, listURL string) (string, string) {
	if u, err := url.Parse(listURL); err == nil && strings.HasPrefix(u.Path, "/search/") {
		kw, derr := url.PathUnescape(strings.TrimPrefix(u.Path, "/search/"))
		if derr == nil && strings.TrimSpace(kw) != "" {
			return nico.SearchProgressKey(kw), "搜索「" + kw + "」"
		}
	}
	return nico.SearchProgressKey(input), "搜索「" + input + "」"
}

func (c *CLI) cmdNicoDown(ctx context.Context, a *archiver.Archiver, args []string) error {
	if len(args) < 1 {
		return usagef("用法: nico-down <静画投稿URL>")
	}
	ill, err := a.ProcessIllust(ctx, args[0])
	if err != nil {
		return err
	}
	c.log.Infof("完成: %s", ill.Summarize())
	c.log.Infof("%s", a.Stats().String())
	return nil
}

func (c *CLI) cmdDown(ctx context.Context, a *archiver.Archiver, args []string) error {
	if len(args) < 1 {
		return usagef("用法: down <getuploader下载页URL> [密码]")
	}
	password := ""
	if len(args) >= 2 {
		password = args[1]
	}
	res, err := a.DirectDownload(ctx, args[0], password)
	if err != nil {
		return err
	}
	if res == nil {
		c.log.Infof("没有需要下载的文件")
		return nil
	}
	c.log.Infof("完成: %s (%s)", res.Path, util.HumanBytes(res.Bytes))
	c.log.Infof("SHA256: %s", res.SHA256)
	return nil
}

// parseCatalogArgs 解析 down-catalog 的位置参数：<url> [密码] [页码]。
//
// 密码可能是纯数字（如 2525），因此不能靠「是不是数字」判断它是密码还是页码，
// 只能按位置解释；只想要页码时用 "-" 占位。
func parseCatalogArgs(args []string) (password string, page int, err error) {
	page = 1
	if len(args) < 1 {
		return "", 0, usagef("用法: down-catalog <上传者目录URL> [密码] [页码]")
	}
	if len(args) >= 2 {
		password = strings.TrimSpace(args[1])
		switch strings.ToLower(password) {
		case "", "-", "none", "--":
			password = ""
		}
	}
	if len(args) >= 3 {
		n, convErr := strconv.Atoi(strings.TrimSpace(args[2]))
		if convErr != nil || n <= 0 {
			return "", 0, usagef("页码必须是正整数: %s", args[2])
		}
		page = n
	}
	if len(args) > 3 {
		return "", 0, usagef("参数过多: %s（用法: down-catalog <目录URL> [密码] [页码]）", strings.Join(args[3:], " "))
	}
	return password, page, nil
}

// cmdDownCatalog 把 getuploader 上传者主页当作目录，下载其中所有文件。
func (c *CLI) cmdDownCatalog(ctx context.Context, a *archiver.Archiver, args []string) error {
	if len(args) < 1 {
		return usagef("用法: down-catalog <上传者目录URL> [密码] [页码]")
	}
	ref, err := getuploader.ParseCatalogURL(args[0])
	if err != nil {
		return err
	}
	password, page, err := parseCatalogArgs(args)
	if err != nil {
		return err
	}

	key := ref.Key()
	explicit := page > 1
	if !explicit && !a.Restart {
		if e, ok := a.ResumePage(key); ok && e.LastPage > 0 {
			note := ""
			if e.Completed {
				note = "，上次已翻到最后一页"
			}
			c.log.Infof("检测到历史进度 %s：上次停在第 %d 页%s，从该页继续（--restart 可从头开始）", key, e.LastPage, note)
			page = e.LastPage
		}
	} else if a.Restart {
		c.log.Infof("已忽略历史进度（--restart），从第 1 页开始")
	}
	if password != "" {
		c.log.Infof("将对该目录内需要密码的文件使用密码: %s", password)
	}
	c.log.Stepf("开始下载上传者目录: %s（%s，从第 %d 页开始）", ref.Root, ref.Account, page)
	return a.ProcessCatalog(ctx, ref, password, page, key, ref.Label())
}

// cmdProgress 查看或清除索引下载进度。
func (c *CLI) cmdProgress(a *archiver.Archiver, args []string) error {
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "clear", "reset":
			if len(args) < 2 {
				return usagef("用法: progress clear <key|all>（progress 查看所有 key）")
			}
			target := args[1]
			if strings.EqualFold(target, "all") || target == "*" {
				if err := a.ClearProgress(""); err != nil {
					return err
				}
				c.log.Infof("已清空全部下载进度")
				return nil
			}
			if err := a.ClearProgress(target); err != nil {
				return err
			}
			c.log.Infof("已清除进度: %s", target)
			return nil
		default:
			return fmt.Errorf("未知 progress 子命令: %s（可用: progress [clear <key|all>]）", args[0])
		}
	}
	entries := a.ProgressEntries()
	fmt.Fprintln(c.out)
	if len(entries) == 0 {
		fmt.Fprintln(c.out, "还没有任何下载进度记录。")
		fmt.Fprintf(c.out, "进度文件：%s\n\n", orDash(a.ProgressPath()))
		return nil
	}
	fmt.Fprintf(c.out, "%-28s %8s  %-8s %s\n", "KEY", "页码", "状态", "更新时间")
	for _, e := range entries {
		st := "进行中"
		if e.Completed {
			st = "已完成"
		}
		fmt.Fprintf(c.out, "%-28s %8d  %-8s %s\n", e.Key, e.LastPage, st, e.UpdatedAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Fprintf(c.out, "\n进度文件：%s\n", orDash(a.ProgressPath()))
	fmt.Fprintln(c.out, "提示：未指定页码时会从记录的页码继续；progress clear <key> 可重置。")
	fmt.Fprintln(c.out)
	return nil
}

func (c *CLI) cmdProbe(ctx context.Context, a *archiver.Archiver) error {
	c.log.Stepf("连通性检查（代理: %s）", orDash(a.ProxyInfo.URL))
	results := a.Probe(ctx)
	allOK := true
	for _, r := range results {
		if r.OK {
			c.log.Infof("  [OK] %s: %s", r.Name, r.URL)
		} else {
			allOK = false
			c.log.Warnf("  [失败] %s: %s -> %s", r.Name, r.URL, r.Detail)
		}
	}
	if !allOK {
		c.log.Warnf("有站点不可达：请检查代理（cfg set proxy ...）或 Cookie。")
	}
	return nil
}

// askPassword 在自动取不到密码时询问用户。
// 用户按 Ctrl+C 时返回 archiver.ErrCanceled，表示放弃这一个下载（回到命令提示符）。
func (c *CLI) askPassword(hint string) (string, error) {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "  ── 需要下载密码（Ctrl+C 跳过此链接）──")
	for _, line := range strings.Split(hint, "\n") {
		fmt.Fprintf(c.out, "  %s\n", line)
	}
	v, err := c.promptPassword("  请输入密码（直接回车跳过）: ")
	if err != nil {
		if errors.Is(err, prompt.ErrInterrupted) {
			fmt.Fprintln(c.out)
			return "", archiver.ErrCanceled
		}
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(v), nil
}

// ---------- cfg ----------

func (c *CLI) cfgCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return c.cfgWizard(ctx)
	}
	switch strings.ToLower(args[0]) {
	case "show", "list":
		c.showConfig()
	case "path":
		fmt.Fprintln(c.out, c.cfgPath)
	case "test":
		a, err := c.ensureArchiver()
		if err != nil {
			return err
		}
		return c.cmdProbe(ctx, a)
	case "set":
		if len(args) < 3 {
			return usagef("用法: cfg set <键> <值>（cfg show 查看可用的键）")
		}
		return c.cfgSet(args[1], strings.Join(args[2:], " "))
	case "reset":
		*c.cfg = *config.Default()
		return c.saveConfig()
	default:
		return fmt.Errorf("未知 cfg 子命令: %s", args[0])
	}
	return nil
}

func (c *CLI) cfgWizard(_ context.Context) error {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "== 配置向导（直接回车保留当前值，Ctrl+C 取消并返回）==")

	// 任一步骤按 Ctrl+C 都放弃整个向导，不保存。
	if err := c.cfgAskAll(); err != nil {
		if errors.Is(err, prompt.ErrInterrupted) {
			fmt.Fprintln(c.out)
			c.log.Warnf("已取消配置，改动未保存。")
			return nil
		}
		return err
	}
	c.cfg.Normalize()
	return c.saveConfig()
}

// cfgAskAll 依次询问所有配置项。
func (c *CLI) cfgAskAll() error {
	if err := c.askInto("下载目录", &c.cfg.DownloadDir, func(v string) error {
		if strings.TrimSpace(v) == "" {
			return errors.New("不能为空")
		}
		return nil
	}); err != nil {
		return err
	}

	pr := proxy.Resolve(c.cfg.Proxy)
	proxyHint := "auto"
	if pr.URL != "" {
		proxyHint = fmt.Sprintf("当前系统代理: %s (%s)", pr.URL, pr.Source)
	}
	fmt.Fprintf(c.out, "  代理说明: auto=读取系统代理，none=直连，也可以直接填 http://host:port\n")
	if err := c.askInto("代理 "+proxyHint, &c.cfg.Proxy, nil); err != nil {
		return err
	}

	fmt.Fprintf(c.out, "  nico Cookie: 浏览器登录 seiga 后，复制请求头里的整段 Cookie（含 user_session=...）\n")
	if err := c.askInto("nico Cookie "+c.cfg.MaskedCookie(), &c.cfg.NicoCookie, nil); err != nil {
		return err
	}

	if err := c.askInto("User-Agent", &c.cfg.UserAgent, nil); err != nil {
		return err
	}

	var delay string
	if err := c.askInto(fmt.Sprintf("请求间隔毫秒（当前 %d）", c.cfg.DelayMS), &delay, nil); err != nil {
		return err
	}
	if delay != "" {
		if n, err := strconv.Atoi(delay); err == nil && n >= 0 {
			c.cfg.DelayMS = n
		} else {
			c.log.Warnf("请求间隔无效，保持原值")
		}
	}
	var timeout string
	if err := c.askInto(fmt.Sprintf("请求超时秒（当前 %d）", c.cfg.TimeoutSec), &timeout, nil); err != nil {
		return err
	}
	if timeout != "" {
		if n, err := strconv.Atoi(timeout); err == nil && n > 0 {
			c.cfg.TimeoutSec = n
		} else {
			c.log.Warnf("超时无效，保持原值")
		}
	}
	var ask string
	cur := "y"
	if !c.cfg.AskPassword {
		cur = "n"
	}
	if err := c.askInto("找不到密码时询问 (y/n) ["+cur+"]", &ask, nil); err != nil {
		return err
	}
	if ask != "" {
		c.cfg.AskPassword = strings.EqualFold(ask, "y") || strings.EqualFold(ask, "yes") || ask == "是"
	}
	var logPath string
	c.askInto("日志文件（相对下载目录或绝对路径，none=关闭；当前 "+orOff(c.cfg.LogFile)+"）", &logPath, nil)
	if logPath != "" {
		if strings.EqualFold(logPath, "none") || strings.EqualFold(logPath, "off") {
			c.cfg.LogFile = ""
		} else {
			c.cfg.LogFile = logPath
		}
	}
	return c.askInto("日志级别 (debug/info/warn/error)", &c.cfg.LogLevel, nil)
}

func (c *CLI) cfgSet(key, value string) error {
	value = strings.TrimSpace(value)
	switch strings.ToLower(key) {
	case "dir", "download_dir", "downloaddir":
		c.cfg.DownloadDir = value
	case "proxy":
		c.cfg.Proxy = value
	case "cookie", "nico_cookie", "nicocookie":
		c.cfg.NicoCookie = value
	case "ua", "user_agent", "useragent":
		c.cfg.UserAgent = value
	case "delay", "delay_ms":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("delay 需要非负整数: %s", value)
		}
		c.cfg.DelayMS = n
	case "timeout", "timeout_sec":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return fmt.Errorf("timeout 需要正整数: %s", value)
		}
		c.cfg.TimeoutSec = n
	case "ask", "ask_password":
		c.cfg.AskPassword = strings.EqualFold(value, "on") || strings.EqualFold(value, "true") || strings.EqualFold(value, "yes") || value == "1"
	case "max_pages":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return fmt.Errorf("max_pages 需要正整数: %s", value)
		}
		c.cfg.MaxPages = n
	case "log_level", "level":
		lv := strings.ToLower(value)
		switch lv {
		case "debug", "info", "warn", "error":
			c.cfg.LogLevel = lv
		default:
			return fmt.Errorf("日志级别只能是 debug/info/warn/error")
		}
	case "log", "log_file", "logfile":
		switch strings.ToLower(value) {
		case "", "none", "off", "false", "no":
			c.cfg.LogFile = ""
		default:
			c.cfg.LogFile = value
		}
	case "metadata", "metadata_file":
		c.cfg.MetadataFile = value
	default:
		return fmt.Errorf("未知配置键: %s", key)
	}
	c.cfg.Normalize()
	return c.saveConfig()
}

func (c *CLI) saveConfig() error {
	path, err := c.cfg.Save()
	if err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}
	c.cfgPath = path
	c.rebuild()
	c.attachLogFile()
	c.log.Infof("配置已保存: %s", path)
	return nil
}

func (c *CLI) showConfig() {
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "配置文件      : %s\n", orDash(c.cfgPath))
	fmt.Fprintf(c.out, "download_dir  : %s\n", c.cfg.DownloadDir)
	fmt.Fprintf(c.out, "metadata_file : %s（绝对路径: %s）\n", c.cfg.MetadataFile, c.cfg.MetadataPath())
	fmt.Fprintf(c.out, "log_file      : %s（绝对路径: %s）\n", orOff(c.cfg.LogFile), orDash(c.cfg.LogPath()))
	fmt.Fprintf(c.out, "proxy         : %s\n", c.cfg.Proxy)
	pr := proxy.Resolve(c.cfg.Proxy)
	if pr.URL == "" {
		fmt.Fprintf(c.out, "  解析结果    : 直连 (%s)\n", pr.Source)
	} else {
		fmt.Fprintf(c.out, "  解析结果    : %s (%s)\n", pr.URL, pr.Source)
	}
	fmt.Fprintf(c.out, "nico_cookie   : %s\n", c.cfg.MaskedCookie())
	fmt.Fprintf(c.out, "user_agent    : %s\n", c.cfg.UserAgent)
	fmt.Fprintf(c.out, "delay_ms      : %d\n", c.cfg.DelayMS)
	fmt.Fprintf(c.out, "timeout_sec   : %d\n", c.cfg.TimeoutSec)
	fmt.Fprintf(c.out, "ask_password  : %v\n", c.cfg.AskPassword)
	fmt.Fprintf(c.out, "max_pages     : %d\n", c.cfg.MaxPages)
	fmt.Fprintf(c.out, "log_level     : %s\n", c.cfg.LogLevel)
	fmt.Fprintln(c.out)
}

func (c *CLI) askInto(label string, dst *string, validate func(string) error) error {
	fmt.Fprintf(c.out, "  %s\n", label)
	v, err := c.promptLine("    > ")
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if validate != nil {
		if err := validate(v); err != nil {
			c.log.Warnf("%v（保留原值）", err)
			return nil
		}
	}
	*dst = v
	return nil
}

// ---------- help ----------

func (c *CLI) showHelp(args []string) {
	topic := ""
	if len(args) > 0 {
		topic = strings.ToLower(args[0])
	}
	switch topic {
	case "nico-search", "search":
		fmt.Fprint(c.out, `
nico-search <搜索页URL|关键词> [起始页]
  用 nico seiga 搜索页作为索引，一页页地处理投稿。
  - 只给关键词时会自动构造搜索地址。
  - 不写起始页时，若有历史进度就从记录的页码继续（--restart 可从头开始）。
  - 若遇到需要登录的页面，请先在 cfg 中设置 nico_cookie。
  例：
    nico-search https://seiga.nicovideo.jp/search/ボイロ?target=illust 1
    nico-search ボイロ
`)
	case "nico-tag", "tag":
		fmt.Fprint(c.out, `
nico-tag <tag|tag页面URL> [起始页]
  用 nico seiga 的 tag 页面作为索引批量下载。
  - 只给 tag 名时会自动构造地址（也可直接贴 URL）。
  - 不写起始页时，若有历史进度就从记录的页码继续（--restart 可从头开始）。
  例：
    nico-tag VOICEROID
    nico-tag 立ち絵 3
    nico-tag https://seiga.nicovideo.jp/tag/VOICEROID
`)
	case "nico-author", "author":
		fmt.Fprint(c.out, `
nico-author <作者主页URL> [起始页]
  用作者主页作为索引，翻完作者的所有投稿。
  - 不写起始页时，若有历史进度就从记录的页码继续（--restart 可从头开始）。
  例：
    nico-author https://seiga.nicovideo.jp/user/illust/39911624
`)
	case "progress":
		fmt.Fprint(c.out, `
progress                    查看各索引（搜索 / tag / 作者）的下载进度
progress clear <key>        清除某个索引的进度
progress clear all          清除全部进度

进度文件保存在配置目录下，每处理完一页就更新一次。
未指定页码时，命令会从记录的页码继续下载（已下载过的文件会自动跳过）。
`)
	case "nico-down", "nico":
		fmt.Fprint(c.out, `
nico-down <静画投稿URL>
  只处理一个投稿：读取简介 -> 找出所有 getuploader 链接 -> 下载。
  例：
    nico-down https://seiga.nicovideo.jp/seiga/im11691748
`)
	case "down":
		fmt.Fprint(c.out, `
down <getuploader下载页URL> [密码]
  跳过 nico，直接下载一个 getuploader 链接。没有密码时会自动尝试。
  - 密码先按原样提交；若失败且含有全角字符，会自动转成半角再试一次。
  例：
    down https://uu.getuploader.com/harinashi/download/71 2525
    down https://uu.getuploader.com/harinashi/download/71 ２５２５
`)
	case "down-catalog", "catalog":
		fmt.Fprint(c.out, `
down-catalog <上传者目录URL> [密码] [页码]
  把 getuploader 的上传者主页当作目录，逐页下载其中的所有文件。
  - 密码会用于该目录下所有需要密码的文件；省略时按需询问。
  - 参数按顺序解释：第二个是密码，第三个才是页码（密码可能是纯数字）。
    只想要页码时用 "-" 占位。
  - 不写页码时，若有历史进度就从记录的页码继续（--restart 可从头开始）。
  - 文件保存到 <下载目录>/<上传者账号>/。
  例：
    down-catalog https://ux.getuploader.com/SHINOBILI7/
    down-catalog https://ux.getuploader.com/SHINOBILI7/ 2525
    down-catalog https://ux.getuploader.com/SHINOBILI7/ 2525 2
    down-catalog https://ux.getuploader.com/SHINOBILI7/ - 2
    archiver https://ux.getuploader.com/SHINOBILI7/ 2525 2
`)
	case "cfg", "config":
		fmt.Fprint(c.out, `
cfg                        进入配置向导
cfg show                   查看当前配置（含代理解析结果）
cfg set <键> <值>          修改单项配置，例如：
                             cfg set proxy auto
                             cfg set proxy http://127.0.0.1:7897
                             cfg set cookie "user_session=..."
                             cfg set dir ~/Downloads/seiga
                             cfg set delay 2000
cfg path                   打印配置文件路径
cfg test                   检查 nico / getuploader 连通性
cfg reset                  恢复默认配置
`)
	case "keys", "keyboard":
		fmt.Fprint(c.out, `
键盘操作（需要 stdin 是终端）：
  ↑ / ↓            翻阅命令历史（跨会话保存）
  ← / →            移动光标        Home/End (Ctrl+A/Ctrl+E)  行首/行尾
  Ctrl+W           删除一个词      Ctrl+K   删除光标到行尾
  Ctrl+R           反向搜索历史     Ctrl+Y   粘贴上次删除的内容
  Tab              补全命令名
  Ctrl+C           取消当前操作 / 返回上级 / 退出
`)
	default:
		fmt.Fprintf(c.out, `
交互模式（archiver>）可用命令：
  nico-search <url|关键词> [起始页]  用搜索页作为索引批量下载
  nico-tag <tag|url> [起始页]       用 tag 页面作为索引批量下载
  nico-author <url> [起始页]        用作者主页作为索引批量下载
  nico-down <url>                   处理单个 nico 静画投稿
  down <url> [密码]                 直接下载一个 getuploader 链接
  down-catalog <url> [密码] [页码]  把 getuploader 上传者主页当作目录批量下载
  cfg [show|set|path|test|reset]    配置（Cookie / 下载目录 / 代理等）
  progress [clear <key|all>]        查看 / 清除索引下载进度
  probe                             检查 nico / getuploader 连通性
  stats                             显示本次会话的统计
  help [命令]                       查看详细帮助（help keys 查看快捷键）
  exit | quit                       退出

删除线链接：
  简介里被删除线划掉的链接（<s>/<del>/<strike>、style 含 line-through、~~文本~~）
  会自动跳过，只使用没划线的链接；跳过记录会写进日志。

命令行模式（不进 REPL，执行完就退出）：
  archiver <命令> [参数...]         例如 archiver nico-tag VOICEROID 2
  archiver <URL>                    直接粘贴链接，自动识别功能
  archiver --help                   查看全部参数与示例
  交互模式里的通用参数在这里同样可用，且可以放在命令前后任意位置。

页码与续传：
  不写起始页时，如果该索引有历史进度，就从记录的页码继续下载；
  想强制从头开始用 --restart，想重置记录用 progress clear <key>。

密码处理：
  密码保留作者原本的写法（全角「２５２５」与半角「2525」并不等价）。
  先按原样提交；若认证失败且密码含全角字符，会自动转成半角再试一次；
  仍然失败才询问你手动输入。

  识别的密码关键词（不区分大小写）：
    日文  パスワード / パスワードは / パスコード / パス / 合言葉 / 暗証番号 / キーコード / キー
    英文  password / pass / passwd / passcode / pwd / pw / key
  关键词后面可以跟 は / を / : / = / → / 括号等，也可以换行后再写密码。

Ctrl+C 的行为：
  - 任务运行中    取消当前任务，回到 archiver> 提示符
  - 配置向导中    放弃本次配置，改动不保存，回到提示符
  - 密码询问中    跳过这一个链接，回到提示符
  - 在提示符处    退出程序（等同 exit）

通用参数：
  --force / -f   即使文件已存在也重新下载
  --dry-run / -n 只扫描并报告将要下载的链接，不真正下载
  --restart      忽略历史进度，从第 1 页开始
  --debug        输出调试日志（HTTP 请求等）
  更多参数（-d/--dir、-p/--proxy、--cookie、--delay、--timeout、-q）见 archiver --help

下载文件会保存到：<下载目录>/<作者名>/<文件名>
元数据（nico 地址、标题、作者、getuploader 地址、文件名、路径、MD5、SHA256）
会写入：%s
日志会追加写入：%s（可用 cfg set log none 关闭）

提示：nico seiga 需要日本代理；本工具默认读取系统代理（cfg set proxy auto）。
`, c.cfg.MetadataPath(), orDash(c.cfg.LogPath()))
	}
	fmt.Fprintln(c.out)
}

// ---------- 小工具 ----------

func parseLevel(s string) logx.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return logx.LevelDebug
	case "warn", "warning":
		return logx.LevelWarn
	case "error":
		return logx.LevelError
	default:
		return logx.LevelInfo
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func orOff(s string) string {
	if strings.TrimSpace(s) == "" {
		return "off"
	}
	return s
}
