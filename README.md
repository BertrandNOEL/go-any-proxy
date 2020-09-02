你好！
很冒昧用这样的方式来和你沟通，如有打扰请忽略我的提交哈。我是光年实验室（gnlab.com）的HR，在招Golang开发工程师，我们是一个技术型团队，技术氛围非常好。全职和兼职都可以，不过最好是全职，工作地点杭州。
我们公司是做流量增长的，Golang负责开发SAAS平台的应用，我们做的很多应用是全新的，工作非常有挑战也很有意思，是国内很多大厂的顾问。
如果有兴趣的话加我微信：13515810775  ，也可以访问 https://gnlab.com/，联系客服转发给HR。
**This is fork project from https://github.com/ryanchapman/go-any-proxy, but add a few features.**

# Transparent Proxy

transparent-proxy is a server that can transparently proxy any tcp connection through an upstream proxy server.  This type
of setup is common in corporate environments.  It is written in golang and has been load tested with 10,000 concurrent
connections successfully on a Vyatta running a 64-bit kernel.

## More info

For more info, see original post http://blog.rchapman.org/post/47406142744/transparently-proxying-http-and-https-connections

## Authentication

You can add basic authentication parameters if needed, like this:

`any_proxy -l :3140 -p "MyLogin:Password25@proxy.corporate.com:8080"`

## Install Info 
You may need to run `go get` for library dependencies.

## Docker Integration (**New**)
This project support docker and integrated iptables for container start/stop.

Additional features supported:
- Use DNS proxy to intercept DNS resolving request if "DNS_PORT" is enabled
- Advanced proxy routing based on configuration file for multiple proxy servers on ip/ip-net/domain-pattern

Using the following command to start transparent proxy on host:
```
docker run -it --rm --privileged -e HTTP_PROXY=proxy.corporate.com:8080 -e NO_PROXY=192.176.0.1/8,172.10.0.1/8 -e LISTEN_PORT=3129 -e IPTABLE_MARK=2515 -e PROXY_PORTS=80,443,22 -e DNS_PORT=53 --net=host fengzhou/transparent-proxy
```

The options are important for run docker:
* "--privileged": This option is required for application to set socket options for SO_MARK.
* "--net=host": This option is required to setup iptables inside of container, and get original ip/port when get connection redirected from iptables.
* Env "HTTP_PROXY": This is upstream proxy, should support "CONNECT" HTTP method (Usually it is http proxy for HTTPS).
* Env "NO_PROXY": This is optional value to by pass network address without go through upstream proxy.
* Env "LISTEN_PORT": This is optional value and it can be any open port. Default value is "3129".
* Env "IPTABLE_MASK": This is optional value, and should be different with other mark value used in iptables. Default value is "5".
* Env "PROXY_PORTS": This is optional value for ports that can be transparent to proxy. The default value is "80,443".
* Env "DNS_PORT": This is optional value to start DNS proxy to help advanced filter by DNS. Default value is "0" (disable). Use "53" to enable it.
* Env "PROXY_CONFIG_FILE": This is optional value to use advanced proxy routing configuration file. Default value is disabled.


## Proxy Configuration File
To enable advanced proxy routing, "-df" option for application, or "PROXY_CONFIG_FILE" environment for docker can be used.

Here is sample configuration file.
```yaml
---
proxy:
rules:
  - 10.0.0.0/8
  - 192.168.0.0/16
  - '*.example.net'
---
proxy: proxy1.example.com:8080
rules:
  - '172.168.1.0/16'
  - '*.example.com'
  - '*.example.*'
---
proxy: proxy2.example.com:8080
rules:
  - '*.net'
```

Each proxy routing is separated by `---`. For proxy with empty value, it is special rule for additional direct connect (no proxy).
For others, it will be tested from top to bottom and use if ip/ip-net/domain matching. If nothing matched, use default proxy in "HTTP_PROXY" environment.

To support the domain-based rule, the "DNS_PORT" should be explicitly enabled.
