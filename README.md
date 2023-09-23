# Hlash

```shell
hlash -d /path/to/data
```

```yaml
# /path/to/data/config.yaml

subscribe:
  - name: mySubscribe-01
    url: https://url/to/subscribe
    cron: "@every 24h"
```

```yaml
# /path/to/data/general.yaml

# 混合端口
mixed-port: 7890

# HTTP 代理端口
port: 0

# SOCKS5 代理端口
socks-port: 0

# Linux 和 macOS 的 redir 代理端口
redir-port: 7892

# 允许局域网的连接
allow-lan: true

# 规则模式：Rule（规则） / Global（全局代理）/ Direct（全局直连）
mode: rule

# 设置日志输出级别 (默认级别：silent，即不输出任何内容，以避免因日志内容过大而导致程序内存溢出）。
# 5 个级别：silent / info / warning / error / debug。级别越高日志输出量越大，越倾向于调试，若需要请自行开启。
log-level: info

# Clash 的 RESTful API
external-controller: "0.0.0.0:9090"

# RESTful API 的口令
secret: ""

# 您可以将静态网页资源（如 clash-dashboard）放置在一个目录中，clash 将会服务于 `RESTful API/ui`
# 参数应填写配置目录的相对路径或绝对路径。
external-ui: ui
```

```shell
# build
git clone github.com/hxnas/hlash.git
cd hlash
go run -tags build build.go
```
