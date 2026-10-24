package cli

import (
	"fmt"
	"strconv"
	"strings"

	"nico-seiga-archiver/internal/nico"
	"nico-seiga-archiver/internal/proxy"
	"nico-seiga-archiver/internal/util"
)

// options 是从命令行解析出来的全局参数。
type options struct {
	force   bool
	dryRun  bool
	restart bool
	quiet   bool
	debug   bool

	// 覆盖配置文件里的值；空字符串/0 表示不覆盖。
	dir       string
	proxy     string
	cookie    string
	delay     int
	timeout   int
	userAgent string
	// logFile 覆盖日志文件路径；noLog 表示本次运行不写日志文件。
	logFile string
	noLog   bool

	// 其余参数（命令 + 命令参数）。
	argv []string
}

// valueFlags 列出需要带一个值的参数及其归一化名称。
var valueFlags = map[string]string{
	"-d": "dir", "--dir": "dir", "--output": "dir",
	"-p": "proxy", "--proxy": "proxy",
	"--cookie": "cookie",
	"-c":       "config", "--config": "config",
	"--delay":   "delay",
	"--timeout": "timeout",
	"--ua":      "ua", "--user-agent": "ua",
	"--log": "log", "--log-file": "log",
}

// parseOptions 扫描全部参数，提取全局 flag；不在列表里的原样保留为命令参数。
//
// 支持 `--flag value` 与 `--flag=value` 两种写法。
func parseOptions(argv []string) (options, error) {
	var o options
	i := 0
	for i < len(argv) {
		arg := argv[i]
		if arg == "--" {
			o.argv = append(o.argv, argv[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			o.argv = append(o.argv, arg)
			i++
			continue
		}

		name, inline, hasInline := strings.Cut(arg, "=")
		switch name {
		case "-f", "--force":
			o.force = true
			i++
			continue
		case "-n", "--dry-run", "--dry":
			o.dryRun = true
			i++
			continue
		case "--restart":
			o.restart = true
			i++
			continue
		case "-q", "--quiet", "--silent":
			o.quiet = true
			i++
			continue
		case "--no-log", "--no-logfile":
			o.noLog = true
			i++
			continue
		case "--debug", "--verbose":
			o.debug = true
			i++
			continue
		case "-h", "--help":
			o.argv = append(o.argv, "help")
			i++
			continue
		case "-v", "--version", "-V":
			o.argv = append(o.argv, "version")
			i++
			continue
		}

		key, ok := valueFlags[name]
		if !ok {
			return o, fmt.Errorf("未知参数: %s（输入 archiver --help 查看用法）", arg)
		}

		value := inline
		if !hasInline {
			if i+1 >= len(argv) {
				return o, fmt.Errorf("参数 %s 需要一个值", name)
			}
			value = argv[i+1]
			i++
		}
		i++

		switch key {
		case "dir":
			o.dir = value
		case "proxy":
			o.proxy = value
		case "cookie":
			o.cookie = value
		case "ua":
			o.userAgent = value
		case "log":
			o.logFile = value
		case "config":
			// 配置文件路径由 main 在启动前处理；这里接受但忽略，便于统一写法。
		case "delay":
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return o, fmt.Errorf("--delay 需要非负整数: %s", value)
			}
			o.delay = n + 1 // +1 用于区分「未设置(0)」与「显式设为 0」
		case "timeout":
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n <= 0 {
				return o, fmt.Errorf("--timeout 需要正整数: %s", value)
			}
			o.timeout = n
		}
	}
	return o, nil
}

// apply 把命令行参数覆盖到配置上。返回值表示配置是否发生了变化（需要重建 Archiver）。
func (o options) apply(c *CLI) bool {
	changed := false
	if o.dir != "" {
		c.cfg.DownloadDir = util.ExpandHome(o.dir)
		changed = true
	}
	if o.proxy != "" {
		c.cfg.Proxy = o.proxy
		changed = true
	}
	if o.cookie != "" {
		c.cfg.NicoCookie = o.cookie
		changed = true
	}
	if o.userAgent != "" {
		c.cfg.UserAgent = o.userAgent
		changed = true
	}
	if o.noLog {
		c.cfg.LogFile = ""
		changed = true
	}
	if o.logFile != "" {
		c.cfg.LogFile = util.ExpandHome(o.logFile)
		changed = true
	}
	if o.delay > 0 {
		c.cfg.DelayMS = o.delay - 1
		changed = true
	}
	if o.timeout > 0 {
		c.cfg.TimeoutSec = o.timeout
		changed = true
	}
	switch {
	case o.debug:
		c.cfg.LogLevel = "debug"
	case o.quiet:
		c.cfg.LogLevel = "warn"
	}
	if changed || o.debug || o.quiet {
		c.cfg.Normalize()
		c.rebuild()
		// 日志文件在 executeArgs 里统一启用（help/version 之类不需要）。
	}
	return changed
}

// isKnownCommand 判断是否是已支持的命令名（含别名）。
func isKnownCommand(s string) bool {
	switch strings.ToLower(s) {
	case "help", "?", "h", "version", "exit", "quit", "q", "clear", "cls",
		"nico-search", "search", "ns",
		"nico-tag", "tag", "nt",
		"nico-author", "author", "na",
		"nico-down", "nico", "nd",
		"down", "d",
		"down-catalog", "catalog", "dc",
		"cfg", "config", "progress", "probe", "test", "stats":
		return true
	}
	return false
}

// inferCommand 允许直接粘贴 URL 调用对应功能：
//
//	archiver https://seiga.nicovideo.jp/tag/VOICEROID
//	archiver https://seiga.nicovideo.jp/seiga/im11691748
//
// 等价于显式写 nico-tag / nico-down。
func inferCommand(argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	first := strings.TrimSpace(argv[0])
	if first == "" || isKnownCommand(first) || strings.HasPrefix(first, "-") {
		return argv
	}
	low := strings.ToLower(first)

	prefix := func(cmd string) []string {
		out := make([]string, 0, len(argv)+1)
		out = append(out, cmd)
		out = append(out, argv...)
		return out
	}

	switch {
	case strings.Contains(low, "getuploader.com/") && strings.Contains(low, "/download/"):
		return prefix("down")
	case strings.Contains(low, "getuploader.com/"):
		// 上传者主页（目录）—— 例如 https://ux.getuploader.com/SHINOBILI7/
		return prefix("down-catalog")
	case strings.Contains(low, "/tag/"):
		return prefix("nico-tag")
	case strings.Contains(low, "/user/illust/"):
		return prefix("nico-author")
	case strings.Contains(low, "/seiga/im"):
		return prefix("nico-down")
	case strings.HasPrefix(low, "im") && isDigits(low[2:]):
		return prefix("nico-down")
	case isDigits(low) && len(low) >= 6:
		return prefix("nico-down")
	}
	return argv
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// printUsage 输出命令行模式的用法。
func (c *CLI) printUsage() {
	pr := proxy.Resolve(c.cfg.Proxy)
	fmt.Fprintf(c.out, `nico-seiga-archiver %s

用法：
  archiver                              进入交互模式（archiver> 提示符）
  archiver <命令> [参数...]             执行一条命令后退出
  archiver <URL> [参数...]              直接粘贴 nico 链接，自动识别功能
  archiver --help                       显示本帮助
  archiver --version                    显示版本

命令：
  nico-search <url|关键词> [页码]       用搜索页作为索引批量下载
  nico-tag <tag|url> [页码]             用 tag 页面作为索引批量下载
  nico-author <url> [页码]              用作者主页作为索引批量下载
  nico-down <url>                       处理单个 nico 静画投稿
  down <url> [密码]                     直接下载一个 getuploader 链接
  down-catalog <url> [密码] [页码]      把 getuploader 上传者主页当作目录批量下载
  cfg <show|set|path|test|reset>         配置（交互式 cfg 向导请进入 REPL）
  progress [clear <key|all>]             查看 / 清除索引下载进度
  probe                                 检查 nico / getuploader 连通性
  stats                                 显示本次运行的统计

全局参数（可放在命令前后任意位置）：
  -d, --dir <路径>       覆盖下载目录
  -p, --proxy <值>       覆盖代理：auto / none / http://host:port
      --cookie <值>      覆盖 nico Cookie
      --delay <毫秒>     覆盖请求间隔
      --timeout <秒>     覆盖请求超时
      --ua <值>          覆盖 User-Agent
      --log <路径>       指定日志文件（相对下载目录或绝对路径）
      --no-log           本次运行不写日志文件
  -f, --force            即使文件已存在也重新下载
  -n, --dry-run          只扫描并报告，不真正下载
      --restart          忽略历史进度，从第 1 页开始
  -q, --quiet            只输出警告与错误
      --debug            输出调试日志
  -c, --config <路径>    使用指定的配置文件

当前配置：
  配置文件  : %s
  下载目录  : %s
  代理      : %s (%s)
  日志文件  : %s

退出码：
  0 成功   1 出错   2 参数错误   130 用户取消（Ctrl+C）

示例：
  archiver nico-tag VOICEROID 2 --dry-run
  archiver nico-author https://seiga.nicovideo.jp/user/illust/39911624 -q
  archiver https://seiga.nicovideo.jp/seiga/im11691748
  archiver down https://uu.getuploader.com/harinashi/download/71 2525
  archiver down-catalog https://ux.getuploader.com/SHINOBILI7/ 2525 2
  archiver nico-tag 立ち絵 -d ~/seiga --proxy http://127.0.0.1:7897

`, version, orDash(c.cfgPath), c.cfg.DownloadDir, orDash(pr.URL), pr.Source, orDash(c.cfg.LogPath()))
	fmt.Fprintln(c.out)
}

// ensureConfigFlag 支持在 main 里提前处理 --config（配置路径需要在读取配置前确定）。
func ExtractConfigFlag(argv []string) (string, []string) {
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			break
		}
		name, inline, hasInline := strings.Cut(arg, "=")
		if name != "-c" && name != "--config" {
			continue
		}
		if hasInline {
			return inline, append(append([]string{}, argv[:i]...), argv[i+1:]...)
		}
		if i+1 < len(argv) {
			path := argv[i+1]
			rest := append([]string{}, argv[:i]...)
			rest = append(rest, argv[i+2:]...)
			return path, rest
		}
	}
	return "", argv
}

// hintForUnknown 给未知命令一个更有用的提示。
func hintForUnknown(cmd string) string {
	if cmd == "" {
		return "缺少命令"
	}
	if u, _, err := nico.NormalizeTagURL(cmd); err == nil && u != "" && !strings.Contains(cmd, "://") && !strings.Contains(cmd, "/") {
		return fmt.Sprintf("未知命令: %s\n  想按 tag 批量下载？试试: nico-tag %s\n  想按关键词搜索？试试: nico-search %s", cmd, cmd, cmd)
	}
	return fmt.Sprintf("未知命令: %s（输入 help 查看帮助）", cmd)
}
