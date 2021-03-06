package main

import (
	logger "github.com/zdannar/flogger"
	"github.com/miekg/dns"
	"os"
	"bufio"
	"io"
	"fmt"
	"errors"
	"strings"
	"net"
	"syscall"
	"time"
	"strconv"
)

type dnsProxy struct {
	addr       string
	remoteAddr string
	dnsServer  *dns.Server
}

var iptablesIntegration = os.Getuid() == 0

func NewDnsProxy(addr, remoteAddr string) *dnsProxy {
	proxy := new(dnsProxy)
	proxy.addr = addr
	proxy.remoteAddr = remoteAddr
	h := proxy.newDnsHandler()
	proxy.dnsServer = &dns.Server{Addr: addr, Net: "udp", Handler: h}
	return proxy
}

func (proxy *dnsProxy) newDnsHandler() dns.Handler {
	return dns.HandlerFunc(func(w dns.ResponseWriter, m *dns.Msg) {
		logger.Debugf("Got question %v", m.Question)
		var qName string
		for _, qm := range m.Question {
			qName = qm.Name
		}
		var remoteAddr string
		if iptablesIntegration {
			var err error
			if remoteAddr, err = getOriginalUdpDst(w); err != nil {
				if proxy.remoteAddr == "" {
					logger.Errorf("Abort DNS resolving for no origin dist: %v", err)
					dns.HandleFailed(w, m)
					return
				} else {
					logger.Warningf("Cannot get origin dist, use default %v as remote address: %v", proxy.remoteAddr, err)
				}
				remoteAddr = proxy.remoteAddr
			}
		} else {
			remoteAddr = proxy.remoteAddr
		}
		if r, err := doDnsExchange(m, remoteAddr); err != nil {
			logger.Warningf("failed query remote dns %q: %v", remoteAddr, err)
			dns.HandleFailed(w, m)
		} else {
			if r.Rcode != dns.RcodeSuccess {
				logger.Debugf("failed query %v: status=%v, id=%v", m.Question, dns.RcodeToString[r.Rcode], r.Id)
			} else {
				logger.Debugf("Get reply for query %v", m.Question)
			}
			for _, am := range r.Answer {
				h := am.Header()
				var ip string
				switch rr := am.(type) {
				case *dns.A:
					ip = rr.A.String()
				default:
					ip = ""
				}
				if h.Rrtype == dns.TypeA && h.Class == dns.ClassINET && qName != "" && ip != "" {
					logger.Debugf("Store ip %v for hostname %v with ttl %v+300", ip, qName, int(h.Ttl))
					gReverseLookupCache.storeTtl(ip, qName, int(h.Ttl+300))
				}
			}
			// make sure compress before write msg to keep len <= 512 (DNS UDP size limit)
			r.Compress = true
			w.WriteMsg(r)
		}
	})
}
func doDnsExchange(msg *dns.Msg, remoteAddr string) (*dns.Msg, error) {
	conn, err := dialUdp(remoteAddr)
	if err != nil {
		return nil, err
	}
	localAddr := conn.LocalAddr().String()
	logger.Debugf("Start DNS msg from %v to %v", localAddr, remoteAddr)
	msgSize := uint16(0)
	opt := msg.IsEdns0()
	if opt != nil && opt.UDPSize() >= dns.MinMsgSize {
		msgSize = opt.UDPSize()
	}
	co := &dns.Conn{Conn: conn, UDPSize: msgSize}
	defer co.Close()
	co.SetWriteDeadline(time.Now().Add(dnsTimeout))
	if err = co.WriteMsg(msg); err != nil {
		return nil, err
	}
	co.SetReadDeadline(time.Now().Add(dnsTimeout))
	return co.ReadMsg()
}

const dnsTimeout = 5 * time.Second

func dialUdp(remoteAddr string) (*net.UDPConn, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.IPPROTO_UDP)
	if err != nil {
		return nil, err
	}
	if iptablesIntegration {
		err = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_MARK, gIpTableMark)
		if err != nil {
			logger.Debugf("Cannot set sockopt with mark %v: %v", gIpTableMark, err)
			syscall.Close(fd)
			return nil, err
		}
	}
	ua, err := net.ResolveUDPAddr("udp", remoteAddr)
	if err != nil {
		logger.Errorf("Cannot resolve UDP addr %v: %v", remoteAddr, err)
		syscall.Close(fd)
		return nil, err
	}
	sa := udpAddrToSockaddr(ua)
	err = syscall.Connect(fd, sa)
	if err != nil {
		logger.Errorf("Cannot Connect UDP: %v", err)
		syscall.Close(fd)
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "")
	conn, err := net.FileConn(file)
	// duplicate file created need to close
	if closeErr := file.Close(); closeErr != nil {
		logger.Errorf("Cannot close file %v: %v", fd, closeErr)
	}
	if err != nil {
		logger.Errorf("Cannot create connection by fd %v: %v", fd, err)
		return nil, err
	}
	if udpConn, ok := conn.(*net.UDPConn); ok {
		return udpConn, err
	}
	return nil, errors.New("invalid connection type")
}

func udpAddrToSockaddr(addr *net.UDPAddr) syscall.Sockaddr {
	sa := ipAndZoneToSockaddr(addr.IP, addr.Zone)
	switch sa := sa.(type) {
	case *syscall.SockaddrInet4:
		sa.Port = addr.Port
		return sa
	case *syscall.SockaddrInet6:
		sa.Port = addr.Port
		return sa
	default:
		return nil
	}
}

func getOriginalUdpDst(w dns.ResponseWriter) (string, error) {
	track := new(udpTrack)
	track.r_dst = w.RemoteAddr().String()
	track.proxyPort = strconv.Itoa((w.LocalAddr()).(*net.UDPAddr).Port)
	if err := getMatchedOriginalUdpDst(track); err != nil {
		msg := fmt.Sprintf("cannot get matched original udp dsk from track %v: %v", track, err)
		logger.Debugf(msg)
		return "", errors.New(msg)
	}
	logger.Debugf("Get Original Udp Dst: %v", track.l_dst)
	return track.l_dst, nil
}

func (proxy *dnsProxy) ListenAndServe(block bool) error {
	if !block {
		c := make(chan error)
		proxy.dnsServer.NotifyStartedFunc = func() {
			logger.Debugf("dns server is started at %v.", proxy.dnsServer.PacketConn.LocalAddr())
			c <- nil
			close(c)
		}
		go func() {
			if err := proxy.dnsServer.ListenAndServe(); err != nil {
				logger.Warningf("dns server start error: %v", err)
				c <- err
				close(c)
			}
		}()
		err, ok := <-c
		if !ok {
			return nil
		}
		logger.Debug("finish dns server listen")
		return err
	} else {
		proxy.dnsServer.NotifyStartedFunc = func() {
			logger.Debugf("dns server is started at %v.", proxy.addr)
		}
		return proxy.dnsServer.ListenAndServe()
	}
}

func (proxy *dnsProxy) Close() error {
	logger.Debug("Close dns server")
	return proxy.dnsServer.Shutdown()
}

func getMatchedOriginalUdpDst(ut *udpTrack) error {
	f, err := os.Open("/proc/self/net/nf_conntrack")
	if err != nil {
		if os.IsPermission(err) {
			logger.Debugf("Cannot open file due to permission, use 'root' to get original destination: %v", err)
		} else {
			logger.Errorf("Cannot open file: %v", err)
		}
		return err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	lines := make([]string, 0, 10)
	for {
		line, err := reader.ReadString('\n')
		var track *udpTrack
		if err == io.EOF {
			lines = append(lines, line)
			track = parseConnTrackLine(line)
			if track != nil && track.matches(ut) {
				logger.Debug(line)
				ut.l_dst = track.l_dst
				return nil
			}
			for _, l := range lines {
				logger.Debugf("/proc/self/net/nf_conntrack>> %v", l)
			}
			return errors.New("no original dst found from nf_conntrack")
		} else if err != nil {
			return err
		}
		lines = append(lines, line)
		track = parseConnTrackLine(line)
		if track != nil && track.matches(ut) {
			ut.l_dst = track.l_dst
			return nil
		}
	}
}

type udpTrack struct {
	l_src, l_dst, r_src, r_dst, proxyPort string
}

func parseConnTrackLine(line string) *udpTrack {
	if !strings.Contains(line, " udp ") {
		return nil
	}
	scanner := bufio.NewScanner(strings.NewReader(line))
	scanner.Split(bufio.ScanWords)
	var srcSet, sportSet, dstSet, dportSet bool
	track := new(udpTrack)
	for scanner.Scan() {
		txt := scanner.Text()
		idx := strings.IndexByte(txt, '=')
		if idx > 0 {
			left := txt[:idx]
			right := txt[idx+1:]
			switch {
			case left == "src" && !srcSet:
				srcSet = true
				track.l_src = right
			case left == "src":
				srcSet = true
				track.r_src = right
			case left == "sport" && !sportSet:
				sportSet = true
				track.l_src += ":" + right
			case left == "sport":
				track.r_src += ":" + right
				track.proxyPort = right
			case left == "dst" && !dstSet:
				dstSet = true
				track.l_dst = right
			case left == "dport" && !dportSet:
				dportSet = true
				track.l_dst += ":" + right
			case left == "dst" && dstSet:
				track.r_dst = right
			case left == "dport" && dportSet:
				track.r_dst += ":" + right
			}
		}
	}
	// logger.Debugf("Parse line %q to %q", line, track)
	return track
}

func (track *udpTrack) String() string {
	return fmt.Sprintf("l_src=%v, l_dst=%v, r_src=%v, r_dst=%v, proxyPort=%v", track.l_src, track.l_dst, track.r_src, track.r_dst, track.proxyPort)
}

func (track *udpTrack) matches(another *udpTrack) bool {
	return track.r_dst == another.r_dst && track.proxyPort == another.proxyPort
}
