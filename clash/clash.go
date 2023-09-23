package clash

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "time/tzdata"

	"github.com/Dreamacro/clash/component/mmdb"
	"github.com/Dreamacro/clash/config"
	"github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/dns"
	"github.com/Dreamacro/clash/hub/executor"
	"github.com/Dreamacro/clash/hub/route"
	"github.com/Dreamacro/clash/log"
	"github.com/Dreamacro/clash/tunnel"
	"github.com/robfig/cron/v3"
	"github.com/samber/lo"
	"go.uber.org/automaxprocs/maxprocs"
	"gopkg.in/yaml.v3"
)

func init() {
	maxprocs.Set(maxprocs.Logger(func(string, ...any) {}))
}

const (
	SUBSCRIBE_DIR = "subscribe"
	CLASH_DIR     = "clash"
	CONFIG_FN     = "config.yaml"
)

type Service struct {
	homeDir      string
	config       Config
	curSubscribe *Subscribe

	clash   *config.Config
	dns     *config.DNS
	general *config.General
}

// 配置
type Config struct {
	Current   string //当前配置名称
	Subscribe []*Subscribe
}

// 订阅
type Subscribe struct {
	Name    string   //显示名称
	Url     string   //更新链接
	Method  string   //更新时使用的HTTP方法
	Headers []string //更新时使用的HTTP请求头
	Body    string   //更新请求的Body参数
	Cron    string   //更新计划

	updated  time.Time
	schedule cron.Schedule
	next     time.Time
}

func New(homeDir string) *Service {
	return &Service{homeDir: homeDir}
}

// 开始运行
func (s *Service) Run(ctx context.Context) (err error) {
	if s.homeDir, err = filepath.Abs(s.homeDir); err != nil {
		return
	}

	constant.SetHomeDir(s.pathResolve(CLASH_DIR))

	if err = s.load(); err != nil {
		return
	}

	s.loadGeneral()

	if err = s.clashStart(ctx); err != nil {
		return
	}

	s.subscribeRun(ctx)

	<-ctx.Done()
	return
}

func (s *Service) load() (err error) {
	configFn := s.pathResolve(CONFIG_FN)
	if err = readYaml(configFn, &s.config); err != nil {
		return
	}

	//格式化
	lo.ForEach(s.config.Subscribe, func(subscribe *Subscribe, _ int) {
		if subscribe.Name == "" && subscribe.Url != "" {
			u := subscribe.Url
			if strings.Contains(u, "?") {
				u = strings.Split(subscribe.Url, "?")[0]
			}
			subscribe.Name = filepath.Base(u)
		}

		if stat, _ := os.Stat(s.pathResolve(SUBSCRIBE_DIR, subscribe.Name+".yaml")); stat != nil {
			subscribe.updated = stat.ModTime()
		}
	})

	//获取默认的配置
	if len(s.config.Subscribe) > 0 {
		if s.curSubscribe, _ = lo.Find(s.config.Subscribe, nameEq(s.config.Current)); s.curSubscribe == nil {
			s.curSubscribe = s.config.Subscribe[0]
			s.config.Current = s.curSubscribe.Name
		}
	}

	return
}

func (s *Service) clashStart(ctx context.Context) (err error) {
	if err = s.loadSubscribe(ctx); err != nil {
		return
	}

	if err = s.clashRun(); err != nil {
		return
	}
	return
}

func (s *Service) loadGeneral() {
	if c, _ := executor.ParseWithPath(s.pathResolve("dns.yaml")); c != nil && c.DNS != nil {
		s.dns = c.DNS
	}

	if c, _ := executor.ParseWithPath(s.pathResolve("general.yaml")); c != nil && c.General != nil {
		s.general = c.General
	}
}

// 加载订阅
func (s *Service) loadSubscribe(ctx context.Context) (err error) {
	name := s.config.Current

	if name == "" && len(s.config.Subscribe) > 0 {
		name = s.config.Subscribe[0].Name
	}

	if name == "" {
		err = fmt.Errorf("订阅为空")
		return
	}

	fMain := s.pathResolve(SUBSCRIBE_DIR, s.config.Current+".yaml")

	if _, e := os.Stat(fMain); e != nil {
		if !os.IsNotExist(e) {
			err = e
			return
		}

		sub, _ := lo.Find(s.config.Subscribe, nameEq(s.config.Current))
		if sub == nil {
			log.Infoln("[订阅] [%s] 不存在", s.config.Current)
			return
		}

		if !s.subscribeUpdate(ctx, sub) {
			err = fmt.Errorf("更新订阅失败")
			return
		}
	}

	if s.clash, err = executor.ParseWithPath(fMain); err != nil && !os.IsNotExist(err) {
		return
	}

	return
}

// 运行clash
func (s *Service) clashRun() (err error) {
	if err = initMMDB(); err != nil {
		return
	}

	if s.dns != nil {
		s.clash.DNS = s.dns
	}

	if s.general != nil {
		s.clash.General = s.general
	}

	if s.clash.General.ExternalUI != "" {
		route.SetUIPath(s.clash.General.ExternalUI)
	}

	if s.clash.General.ExternalController != "" {
		go route.Start(s.clash.General.ExternalController, s.clash.General.Secret)
	}

	executor.ApplyConfig(s.clash, true)
	return
}

// 按计划更新
func (s *Service) subscribeRun(ctx context.Context) {
	go func() {
		list := lo.Filter(s.config.Subscribe, func(it *Subscribe, _ int) bool { return it.Url != "" && it.Cron != "" })

		lo.ForEach[*Subscribe](list, func(subscribe *Subscribe, index int) {
			if subscribe.schedule == nil && subscribe.Cron != "" {
				subscribe.schedule, _ = cron.ParseStandard(subscribe.Cron)
			}
			if subscribe.next.IsZero() && subscribe.schedule != nil {
				subscribe.next = subscribe.schedule.Next(time.Now())
			}
		})

		for {
			list = lo.Filter(list, func(it *Subscribe, _ int) bool { return it.Url != "" && it.Cron != "" && !it.next.IsZero() })

			nearly := lo.MinBy(list, func(a, b *Subscribe) bool { return a.next.Before(b.next) })
			sleep := -time.Since(nearly.next)
			if sleep < 0 {
				sleep = time.Millisecond
			}
			log.Infoln("[订阅] 最近需要更新: %s, 等待 %s", nearly.Name, sleep)

			select {
			case <-ctx.Done():
				log.Infoln("[订阅] 退出更新")
				return
			case <-time.After(sleep):
				if nearly.next = nearly.schedule.Next(time.Now()); nearly.next.Before(time.Now()) {
					nearly.next = time.Time{}
				}
				log.Infoln("[订阅] [%s] 更新", nearly.Name)
				go s.subscribeUpdate(ctx, nearly)
			}
		}
	}()
}

// 指定链接和名称更新
func (s *Service) subscribeUpdate(ctx context.Context, subscribe *Subscribe) (success bool) {
	if subscribe.Url == "" {
		log.Infoln("[订阅] [%s] 链接为空", subscribe.Name)
		return
	}

	var (
		target = s.pathResolve(SUBSCRIBE_DIR, subscribe.Name+".yaml")
		tempDl = target + ".update"
		backup = target + "-" + time.Now().Format("20060102-150405") + ".backup"

		hasBackup bool
		err       error
	)

	log.Infoln("[订阅] [%s] 下载... %s", subscribe.Name, subscribe.Url)
	if err = download(ctx, subscribe.Method, subscribe.Url, subscribe.Headers, subscribe.Body, tempDl); err != nil {
		log.Infoln("[订阅] 下载失败: %v", err)
		return
	}

	log.Infoln("[订阅] [%s] 检查... %s", subscribe.Name, tempDl)
	if _, err = executor.ParseWithPath(tempDl); err != nil {
		log.Infoln("[订阅] [%s] 检查失败: %v", subscribe.Name, err)
		return
	}

	//备份之前的文件
	if stat, _ := os.Stat(target); stat != nil {
		log.Infoln("[订阅] [%s] 备份... %s => %s", subscribe.Name, filepath.Base(target), filepath.Base(backup))
		if err = os.Rename(target, backup); err != nil {
			log.Infoln("[订阅] [%s] 备份失败: %v", subscribe.Name, err)
			return
		}
		hasBackup = true
	}

	log.Infoln("[订阅] [%s] 写入...", subscribe.Name)
	if err = os.Rename(tempDl, target); err != nil {
		log.Infoln("[订阅] [%s] 写入失败: %v", subscribe.Name, err)
		//回滚
		if hasBackup {
			log.Infoln("[订阅] [%s] 回滚... %s", subscribe.Name, backup)
			if err = os.Rename(backup, target); err != nil {
				log.Infoln("[订阅] [%s] 回滚失败: %v", subscribe.Name, err)
			}
		}
		return
	}

	log.Infoln("[订阅] [%s] 更新完成", subscribe.Name)
	return true
}

func (s *Service) pathResolve(names ...string) string {
	return filepath.Join(append([]string{s.homeDir}, names...)...)
}

func nameEq(name string) func(*Subscribe) bool {
	return func(it *Subscribe) bool { return strings.EqualFold(it.Name, name) }
}

func initMMDB() (err error) {
	downloadMMDB := func(path string) (err error) {
		resp, err := http.Get("https://cdn.jsdelivr.net/gh/Dreamacro/maxmind-geoip@release/Country.mmdb")
		if err != nil {
			return
		}
		defer resp.Body.Close()

		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(f, resp.Body)

		return err
	}

	if _, err = os.Stat(constant.Path.MMDB()); os.IsNotExist(err) {
		log.Infoln("Can't find MMDB, start download")
		if err = downloadMMDB(constant.Path.MMDB()); err != nil {
			err = fmt.Errorf("can't download MMDB: %s", err.Error())
			return
		}
	}

	if !mmdb.Verify() {
		log.Warnln("MMDB invalid, remove and download")
		if err := os.Remove(constant.Path.MMDB()); err != nil {
			return fmt.Errorf("can't remove invalid MMDB: %s", err.Error())
		}

		if err := downloadMMDB(constant.Path.MMDB()); err != nil {
			return fmt.Errorf("can't download MMDB: %s", err.Error())
		}
	}

	return nil
}

func download(ctx context.Context, method, url string, headers []string, data string, saveTo string) (err error) {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}

	client := &http.Client{
		Timeout:   time.Second * 10,
		Transport: transport,
	}

	windowsEdge := func(header http.Header) {
		header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.31")
		header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
		header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6,zh-TW;q=0.5")
		header.Set("Cache-Control", "no-cache")
		header.Set("Pragma", "no-cache")
	}

	var req *http.Request
	var resp *http.Response
	var body io.Reader

	if method == "" {
		method = http.MethodGet
	}

	if method != http.MethodGet && data != "" {
		body = strings.NewReader(data)
	}

	if req, err = http.NewRequestWithContext(ctx, method, url, body); err != nil {
		return
	}

	windowsEdge(req.Header)

	if len(headers) > 0 {
		for _, it := range headers {
			hdr := strings.SplitN(it, "=", 2)
			if len(hdr) == 2 {
				req.Header.Set(hdr[0], hdr[1])
			} else {
				req.Header.Set(hdr[0], "")
			}
		}
	}

	for i := 0; i < 10; i++ {
		if i > 0 {
			sleep := min(time.Second*1<<i, time.Second*15)
			log.Infoln("[下载] 第%d次重试, 等待时间: %s", i, sleep)
			select {
			case <-ctx.Done():
				err = ctx.Err()
				return
			case <-time.After(sleep):
			}
		}
		if resp, err = client.Do(req); err != nil {
			if i < 9 {
				log.Infoln("[下载] 第%d次失败: %v", i, err)
				continue
			}
			return
		}

		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			err = fmt.Errorf(resp.Status)
			if resp.StatusCode > 404 && i < 9 {
				log.Infoln("[下载] 第%d次失败: %s", i, resp.Status)
				continue
			}
			return
		}

		if err = readToFile(resp.Body, saveTo, true); err != nil {
			continue
		}
		break
	}
	return
}

func readToFile(src io.Reader, dstFilename string, overwrite bool) (err error) {
	if err = os.MkdirAll(filepath.Dir(dstFilename), 0755); err != nil {
		return
	}

	var dst *os.File
	if overwrite {
		dst, err = os.Create(dstFilename)
	} else {
		dst, err = os.OpenFile(dstFilename, os.O_RDWR|os.O_CREATE|os.O_EXCL|os.O_TRUNC, 0666)
	}
	if err != nil {
		return
	}
	defer dst.Close()

	if _, err = io.Copy(dst, src); err != nil {
		return
	}
	return
}

func readYaml(fn string, value any) (err error) {
	var data []byte
	if data, err = os.ReadFile(fn); err != nil {
		return
	}
	return yaml.Unmarshal(data, value)
}

// func (s *Service) watch(ctx context.Context) {
// 	cDone := make(chan struct{})
// 	cReload := make(chan struct{}, 1)
// 	go func() {
// 		defer close(cDone)
// 		defer close(cReload)
//
// 		for {
// 			select {
// 			case <-ctx.Done():
// 				log.Infoln("[内核] 已退出")
// 				s.cReload = nil
// 				return
// 			case <-cReload:
// 				if cfg, e := executor.Parse(); e == nil {
// 					log.Infoln("[内核] 重载配置")
// 					executor.ApplyConfig(cfg, true)
// 				} else {
// 					log.Infoln("[内核] 新配置检查不通过: %v", e)
// 				}
// 			}
// 		}
//
// 	}()
//
// 	// s.done = cDone
// 	// s.cReload = cReload
// }

func DNSDefault() func(cfg *config.Config) {
	return func(cfg *config.Config) {
		cfg.General.RedirPort = 7892
		cfg.DNS = &config.DNS{
			Enable: false,
			IPv6:   true,
			NameServer: []dns.NameServer{
				{Addr: "223.5.5.5"},
			},
			Fallback: []dns.NameServer{
				{Net: "tls", Addr: "1.1.1.1:853"},
				{Net: "tcp", Addr: "1.1.1.1:53"},
				{Net: "tcp", Addr: "208.67.222.222:443"},
				{Net: "tls", Addr: "dns.google"},
			},
			FallbackFilter: config.FallbackFilter{},
			Listen:         "0.0.0.0:53",
			EnhancedMode:   constant.DNSMapping,
		}
	}
}

func GeneralDefault() func(cfg *config.Config) {
	return func(cfg *config.Config) {
		cfg.General.MixedPort = 7890
		cfg.General.ExternalController = "0.0.0.0:9090"
		cfg.General.ExternalUI = "dashboard"
		cfg.General.Port = 0
		cfg.General.SocksPort = 0
		cfg.General.AllowLan = true
		cfg.General.Mode = tunnel.Rule
	}
}

func Backward(ctx context.Context) (err error) {
	if runtime.GOOS == "linux" {
		sh := `
iptables -t nat -D PREROUTING -p tcp -j CLASH
iptables -t nat -F CLASH
iptables -t nat -X CLASH
iptables -t nat -F CLASH
	`
		err=exec.CommandContext(ctx, "bash", "-c", sh).Run()
	}
	return
}

func Forward(ctx context.Context, redirPort int) (err error) {
	if runtime.GOOS == "linux" {
		sh := fmt.Sprintf(`
iptables -t nat -N clash
iptables -t nat -A clash -d 0.0.0.0/8 -j RETURN
iptables -t nat -A clash -d 10.0.0.0/8 -j RETURN
iptables -t nat -A CLASH -d 169.254.0.0/16 -j RETURN
iptables -t nat -A clash -d 127.0.0.0/8 -j RETURN
iptables -t nat -A clash -d 172.16.0.0/12 -j RETURN
iptables -t nat -A clash -d 192.168.0.0/16 -j RETURN
iptables -t nat -A CLASH -d 224.0.0.0/4 -j RETURN
iptables -t nat -A CLASH -d 240.0.0.0/4 -j RETURN
iptables -t nat -A clash -p tcp -j RETURN -m mark --mark 0xff
iptables -t nat -A clash -p tcp -j REDIRECT --to-ports %d
iptables -t nat -A PREROUTING -p tcp -j clash
	`, redirPort)
		err=exec.CommandContext(ctx, "bash", "-c", sh).Run()
	}
	return
}
