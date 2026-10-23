// Package cli 实现 `archiver>` 交互式命令行。
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"nico-seiga-archiver/internal/archiver"
	"nico-seiga-archiver/internal/config"
	"nico-seiga-archiver/internal/logx"
	"nico-seiga-archiver/internal/nico"
	"nico-seiga-archiver/internal/proxy"
	"nico-seiga-archiver/internal/util"
)

const version = "0.1.0"

const banner = `
  ┌──────────────────────────────────────────────────────────┐
  │  nico-seiga-archiver  ·  getuploader 立绘抢救工具         │
  └──────────────────────────────────────────────────────────┘
`

// CLI 是交互式命令行状态。
type CLI struct {
	cfg     *config.Config
	cfgPath string
	log     *logx.Logger
	arch    *archiver.Archiver
	in      *bufio.Reader
	out     io.Writer
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
	return &CLI{
		cfg:     cfg,
		cfgPath: path,
		log:     logger,
		in:      bufio.NewReaderSize(os.Stdin, 64*1024),
		out:     os.Stdout,
	}, nil
}

// Run 启动 REPL；args 非空时以非交互方式执行一条命令。
func (c *CLI) Run(args []string) int {
	c.printBanner()

	if len(args) > 0 {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		_, err := c.execute(ctx, strings.Join(args, " "))
		if err != nil {
			c.log.Errorf("%v", err)
			return 1
		}
		return 0
	}

	for {
		line, err := c.prompt("archiver> ")
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(c.out)
				return 0
			}
			c.log.Errorf("读取输入失败: %v", err)
			return 1
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		exit, err := c.execute(rootCtx, line)
		stop()
		if err != nil {
			c.log.Errorf("%v", err)
		}
		if exit {
			return 0
		}
	}
}

// prompt 读取一行输入；在读输入时忽略 Ctrl-C，避免直接退出。
func (c *CLI) prompt(p string) (string, error) {
	fmt.Fprint(c.out, p)
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	line, err := c.in.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (c *CLI) printBanner() {
	fmt.Fprint(c.out, banner)
	fmt.Fprintf(c.out, "  版本 %s\n", version)
	fmt.Fprintf(c.out, "  配置文件  : %s\n", orDash(c.cfgPath))
	fmt.Fprintf(c.out, "  下载目录  : %s\n", c.cfg.DownloadDir)
	fmt.Fprintf(c.out, "  元数据    : %s\n", c.cfg.MetadataPath())
	pr := proxy.Resolve(c.cfg.Proxy)
	if pr.URL == "" {
		fmt.Fprintf(c.out, "  代理      : 直连 (%s)\n", pr.Source)
	} else {
		fmt.Fprintf(c.out, "  代理      : %s (%s)\n", pr.URL, pr.Source)
	}
	if strings.TrimSpace(c.cfg.NicoCookie) == "" {
		fmt.Fprintf(c.out, "  提示      : 未设置 nico Cookie（可选）。遇到仅登录可见的投稿时，输入 cfg 设置。\n")
	}
	fmt.Fprintln(c.out, "  输入 help 查看可用命令。")
	fmt.Fprintln(c.out)
}

func (c *CLI) execute(ctx context.Context, line string) (exit bool, err error) {
	args := util.SplitArgs(line)
	if len(args) == 0 {
		return false, nil
	}

	// 提取通用 flag。
	force := false
	dryRun := false
	filtered := args[:0]
	for _, a := range args {
		switch a {
		case "--force", "-f":
			force = true
		case "--dry-run", "-n":
			dryRun = true
		case "--debug":
			c.cfg.LogLevel = "debug"
			c.rebuild()
		default:
			filtered = append(filtered, a)
		}
	}
	args = filtered
	if len(args) == 0 {
		return false, nil
	}

	cmd := strings.ToLower(args[0])
	rest := args[1:]

	switch cmd {
	case "help", "?", "h", "--help", "-h":
		c.showHelp(rest)
		return false, nil
	case "version", "--version", "-v":
		fmt.Fprintf(c.out, "nico-seiga-archiver %s\n", version)
		return false, nil
	case "exit", "quit", "q":
		return true, nil
	case "clear", "cls":
		fmt.Fprint(c.out, "\x1b[H\x1b[2J")
		return false, nil
	}

	if cmd == "cfg" || cmd == "config" {
		return false, c.cfgCommand(ctx, rest)
	}

	arch, err := c.ensureArchiver()
	if err != nil {
		return false, err
	}
	arch.Force = force
	arch.DryRun = dryRun
	arch.ResetStats()

	switch cmd {
	case "nico-search", "search", "ns":
		return false, c.cmdNicoSearch(ctx, arch, rest)
	case "nico-author", "author", "na":
		return false, c.cmdNicoAuthor(ctx, arch, rest)
	case "nico-down", "nico", "nd":
		return false, c.cmdNicoDown(ctx, arch, rest)
	case "down", "d":
		return false, c.cmdDown(ctx, arch, rest)
	case "probe", "test":
		return false, c.cmdProbe(ctx, arch)
	case "stats":
		c.log.Infof("%s", arch.Stats().String())
		return false, nil
	default:
		c.log.Warnf("未知命令: %s（输入 help 查看帮助）", args[0])
		return false, nil
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
		return errors.New("用法: nico-search <搜索页URL|关键词> [起始页]")
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
	page := 1
	if len(args) >= 2 {
		n, err := strconv.Atoi(args[1])
		if err != nil || n <= 0 {
			return fmt.Errorf("起始页必须是正整数: %s", args[1])
		}
		page = n
	}
	c.log.Stepf("开始搜索索引: %s（从第 %d 页开始）", listURL, page)
	return a.ProcessList(ctx, listURL, page)
}

func (c *CLI) cmdNicoAuthor(ctx context.Context, a *archiver.Archiver, args []string) error {
	if len(args) < 1 {
		return errors.New("用法: nico-author <作者主页URL>")
	}
	pageURL, id, err := nico.NormalizeUserURL(args[0])
	if err != nil {
		return err
	}
	c.log.Stepf("开始作者索引: %s（作者 ID %s）", pageURL, id)
	return a.ProcessList(ctx, pageURL, 1)
}

func (c *CLI) cmdNicoDown(ctx context.Context, a *archiver.Archiver, args []string) error {
	if len(args) < 1 {
		return errors.New("用法: nico-down <静画投稿URL>")
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
		return errors.New("用法: down <getuploader下载页URL> [密码]")
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
func (c *CLI) askPassword(hint string) (string, error) {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "  ── 需要下载密码 ──")
	for _, line := range strings.Split(hint, "\n") {
		fmt.Fprintf(c.out, "  %s\n", line)
	}
	v, err := c.prompt("  请输入密码（直接回车跳过）: ")
	if err != nil && !errors.Is(err, io.EOF) {
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
			return errors.New("用法: cfg set <键> <值>（cfg show 查看可用的键）")
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
	fmt.Fprintln(c.out, "== 配置向导（直接回车保留当前值）==")

	c.askInto("下载目录", &c.cfg.DownloadDir, func(v string) error {
		if strings.TrimSpace(v) == "" {
			return errors.New("不能为空")
		}
		return nil
	})

	pr := proxy.Resolve(c.cfg.Proxy)
	proxyHint := "auto"
	if pr.URL != "" {
		proxyHint = fmt.Sprintf("当前系统代理: %s (%s)", pr.URL, pr.Source)
	}
	fmt.Fprintf(c.out, "  代理说明: auto=读取系统代理，none=直连，也可以直接填 http://host:port\n")
	c.askInto("代理 "+proxyHint, &c.cfg.Proxy, nil)

	fmt.Fprintf(c.out, "  nico Cookie: 浏览器登录 seiga 后，复制请求头里的整段 Cookie（含 user_session=...）\n")
	c.askInto("nico Cookie "+c.cfg.MaskedCookie(), &c.cfg.NicoCookie, nil)

	c.askInto("User-Agent", &c.cfg.UserAgent, nil)

	var delay string
	c.askInto(fmt.Sprintf("请求间隔毫秒（当前 %d）", c.cfg.DelayMS), &delay, nil)
	if delay != "" {
		if n, err := strconv.Atoi(delay); err == nil && n >= 0 {
			c.cfg.DelayMS = n
		} else {
			c.log.Warnf("请求间隔无效，保持原值")
		}
	}
	var timeout string
	c.askInto(fmt.Sprintf("请求超时秒（当前 %d）", c.cfg.TimeoutSec), &timeout, nil)
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
	c.askInto("找不到密码时询问 (y/n) ["+cur+"]", &ask, nil)
	if ask != "" {
		c.cfg.AskPassword = strings.EqualFold(ask, "y") || strings.EqualFold(ask, "yes") || ask == "是"
	}
	c.askInto("日志级别 (debug/info/warn/error)", &c.cfg.LogLevel, nil)

	c.cfg.Normalize()
	return c.saveConfig()
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
	case "metadata", "metadata_file":
		c.cfg.MetadataFile = value
	case "log", "log_level":
		lv := strings.ToLower(value)
		switch lv {
		case "debug", "info", "warn", "error":
			c.cfg.LogLevel = lv
		default:
			return fmt.Errorf("日志级别只能是 debug/info/warn/error")
		}
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
	c.log.Infof("配置已保存: %s", path)
	return nil
}

func (c *CLI) showConfig() {
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "配置文件      : %s\n", orDash(c.cfgPath))
	fmt.Fprintf(c.out, "download_dir  : %s\n", c.cfg.DownloadDir)
	fmt.Fprintf(c.out, "metadata_file : %s（绝对路径: %s）\n", c.cfg.MetadataFile, c.cfg.MetadataPath())
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

func (c *CLI) askInto(label string, dst *string, validate func(string) error) {
	fmt.Fprintf(c.out, "  %s\n    > ", label)
	line, err := c.in.ReadString('\n')
	if err != nil && line == "" {
		return
	}
	v := strings.TrimSpace(strings.TrimRight(line, "\r\n"))
	if v == "" {
		return
	}
	if validate != nil {
		if err := validate(v); err != nil {
			c.log.Warnf("%v（保留原值）", err)
			return
		}
	}
	*dst = v
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
  - 若遇到需要登录的页面，请先在 cfg 中设置 nico_cookie。
  例：
    nico-search https://seiga.nicovideo.jp/search/ボイロ?target=illust 1
    nico-search ボイロ
`)
	case "nico-author", "author":
		fmt.Fprint(c.out, `
nico-author <作者主页URL>
  用作者主页作为索引，翻完作者的所有投稿。
  例：
    nico-author https://seiga.nicovideo.jp/user/illust/39911624
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
  例：
    down https://uu.getuploader.com/harinashi/download/71 2525
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
	default:
		fmt.Fprintf(c.out, `
可用命令：
  nico-search <url|关键词> [起始页]  用搜索页作为索引批量下载
  nico-author <url>                 用作者主页作为索引批量下载
  nico-down <url>                   处理单个 nico 静画投稿
  down <url> [密码]                 直接下载一个 getuploader 链接
  cfg [show|set|path|test|reset]    配置（Cookie / 下载目录 / 代理等）
  probe                             检查 nico / getuploader 连通性
  stats                             显示本次会话的统计
  help [命令]                       查看详细帮助
  exit | quit                       退出

通用参数：
  --force / -f   即使文件已存在也重新下载
  --dry-run / -n 只扫描并报告将要下载的链接，不真正下载
  --debug        输出调试日志（HTTP 请求等）

下载文件会保存到：<下载目录>/<作者名>/<文件名>
元数据（nico 地址、标题、作者、getuploader 地址、文件名、路径、MD5、SHA256）
会写入：%s

提示：nico seiga 需要日本代理；本工具默认读取系统代理（cfg set proxy auto）。
`, c.cfg.MetadataPath())
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
