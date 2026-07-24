package hue

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/mdns"
	"golang.org/x/net/ipv4"
	"hue2mqtt/internal/config"
)

type DiscoveryManager struct {
	cfgManager *config.Manager
	stopChan   chan struct{}
	wg         sync.WaitGroup
	ssdpConn   *net.UDPConn
	mdnsServer *mdns.Server
}

func NewDiscoveryManager(cfgManager *config.Manager) *DiscoveryManager {
	return &DiscoveryManager{
		cfgManager: cfgManager,
	}
}

// Start binds network sockets synchronously and starts background listeners. Returns an error if sockets cannot be bound.
func (d *DiscoveryManager) Start() error {
	cfg := d.cfgManager.GetConfig()
	bridgeID := d.cfgManager.BridgeID()
	localIP := cfg.Bridge.IP
	if localIP == "" {
		localIP = GetLocalIP()
	}

	// Bind UDP port 1900 on all interfaces with SO_REUSEADDR
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				setReuseAddr(fd)
			})
		},
	}
	pConnRaw, err := lc.ListenPacket(context.Background(), "udp4", "0.0.0.0:1900")
	if err != nil {
		slog.Error("SSDP: failed to listen on UDP port 1900 (continuing with mDNS)", "error", err)
	} else if conn, ok := pConnRaw.(*net.UDPConn); ok {
		d.ssdpConn = conn
		// Join the multicast group on all multicast-capable interfaces
		pConn := ipv4.NewPacketConn(conn)
		group := net.IPv4(239, 255, 255, 250)

		interfaces, err := net.Interfaces()
		if err == nil {
			joinedAny := false
			for _, iface := range interfaces {
				if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
					continue
				}
				if err := pConn.JoinGroup(&iface, &net.UDPAddr{IP: group}); err != nil {
					slog.Debug("SSDP: Failed to join multicast group on interface", "name", iface.Name, "error", err)
				} else {
					slog.Info("SSDP: Joined multicast group on interface", "name", iface.Name)
					joinedAny = true
				}
			}
			if !joinedAny {
				slog.Warn("SSDP: Could not join multicast group on any interface")
			}
		}
	}

	// Create mDNS service synchronously
	// Using "DIYHue-" prefix is highly recognized by Hue discovery clients (like Sleep as Android)
	last6 := strings.ToLower(bridgeID)
	if len(last6) > 6 {
		last6 = last6[len(last6)-6:]
	}
	name := fmt.Sprintf("DIYHue-%s", last6)
	serviceType := "_hue._tcp"
	domain := "local."
	txtRecords := []string{
		"modelid=BSB002",
		fmt.Sprintf("bridgeid=%s", bridgeID),
	}

	var ips []net.IP
	if cfg.Bridge.IP != "" {
		if parsed := net.ParseIP(cfg.Bridge.IP); parsed != nil {
			ips = []net.IP{parsed}
		}
	}

	service, err := mdns.NewMDNSService(
		name,
		serviceType,
		domain,
		"",
		cfg.Bridge.HTTPPort,
		ips,
		txtRecords,
	)
	if err != nil {
		_ = d.ssdpConn.Close()
		return fmt.Errorf("mDNS: failed to create service: %w", err)
	}

	server, err := mdns.NewServer(&mdns.Config{Zone: service})
	if err != nil {
		_ = d.ssdpConn.Close()
		return fmt.Errorf("mDNS: failed to start server: %w", err)
	}
	d.mdnsServer = server

	d.stopChan = make(chan struct{})
	d.wg.Add(2)

	// Start SSDP receiver
	go func() {
		defer d.wg.Done()
		d.runSSDP(localIP, cfg.Bridge.HTTPPort, bridgeID, cfg.Bridge.MAC)
	}()

	// Start SSDP Notify broadcaster
	go func() {
		defer d.wg.Done()
		d.broadcastSSDPNotify(localIP, cfg.Bridge.HTTPPort, bridgeID, cfg.Bridge.MAC)
	}()

	slog.Info("mDNS: Advertising service", "name", name, "type", serviceType, "port", cfg.Bridge.HTTPPort, "ip", localIP)
	return nil
}

func (d *DiscoveryManager) Stop() {
	if d.stopChan != nil {
		close(d.stopChan)

		// Close SSDP socket to unblock read loop
		if d.ssdpConn != nil {
			_ = d.ssdpConn.Close()
		}

		// Shutdown mDNS
		if d.mdnsServer != nil {
			_ = d.mdnsServer.Shutdown()
			slog.Info("mDNS: Service stopped")
		}

		d.wg.Wait()
		d.stopChan = nil
	}
}

func GetLocalIP() string {
	interfaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range interfaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ip4 := ipnet.IP.To4(); ip4 != nil {
						ipStr := ip4.String()
						if !strings.HasPrefix(ipStr, "10.88.") && !strings.HasPrefix(ipStr, "172.17.") && !strings.HasPrefix(ipStr, "127.") {
							return ipStr
						}
					}
				}
			}
		}
	}

	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1" // Fallback
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

func (d *DiscoveryManager) runSSDP(bridgeIP string, port int, bridgeID string, mac string) {
	slog.Info("SSDP: Listening for M-SEARCH requests on 239.255.255.250:1900")

	buf := make([]byte, 2048)
	for {
		select {
		case <-d.stopChan:
			return
		default:
			n, srcAddr, err := d.ssdpConn.ReadFrom(buf)
			if err != nil {
				select {
				case <-d.stopChan:
					return
				default:
					slog.Error("SSDP: Read error", "error", err)
					time.Sleep(1 * time.Second)
					continue
				}
			}

			request := string(buf[:n])
			reqLower := strings.ToLower(request)
			if strings.Contains(reqLower, "m-search") {
				slog.Debug("SSDP: Received M-SEARCH request", "from", srcAddr.String(), "raw", request)
				if strings.Contains(reqLower, "ssdp:discover") ||
					strings.Contains(reqLower, "upnp:rootdevice") ||
					strings.Contains(reqLower, "ssdp:all") ||
					strings.Contains(reqLower, "basic:1") ||
					strings.Contains(reqLower, "device:basic") ||
					strings.Contains(reqLower, "hue") ||
					strings.Contains(reqLower, "internetgatewaydevice") {
					go sendSSDPResponse(srcAddr, bridgeIP, port, bridgeID, mac)
				}
			}
		}
	}
}

func sendSSDPResponse(srcAddr net.Addr, bridgeIP string, port int, bridgeID string, mac string) {
	conn, err := net.Dial("udp", srcAddr.String())
	if err != nil {
		return
	}
	defer conn.Close()

	macLower := strings.ToLower(mac)

	// 1. Root device response
	resp1 := fmt.Sprintf("HTTP/1.1 200 OK\r\n"+
		"HOST: 239.255.255.250:1900\r\n"+
		"EXT:\r\n"+
		"CACHE-CONTROL: max-age=100\r\n"+
		"LOCATION: http://%s:%d/description.xml\r\n"+
		"SERVER: Linux/3.14.0 UPnP/1.0 IpBridge/1.20.0\r\n"+
		"hue-bridgeid: %s\r\n"+
		"ST: upnp:rootdevice\r\n"+
		"USN: uuid:2f402f80-da50-11e1-9b23-%s::upnp:rootdevice\r\n\r\n",
		bridgeIP, port, bridgeID, macLower)

	// 2. Specific device UUID response
	resp2 := fmt.Sprintf("HTTP/1.1 200 OK\r\n"+
		"HOST: 239.255.255.250:1900\r\n"+
		"EXT:\r\n"+
		"CACHE-CONTROL: max-age=100\r\n"+
		"LOCATION: http://%s:%d/description.xml\r\n"+
		"SERVER: Linux/3.14.0 UPnP/1.0 IpBridge/1.20.0\r\n"+
		"hue-bridgeid: %s\r\n"+
		"ST: uuid:2f402f80-da50-11e1-9b23-%s\r\n"+
		"USN: uuid:2f402f80-da50-11e1-9b23-%s\r\n\r\n",
		bridgeIP, port, bridgeID, macLower, macLower)

	// 3. Basic device type response
	resp3 := fmt.Sprintf("HTTP/1.1 200 OK\r\n"+
		"HOST: 239.255.255.250:1900\r\n"+
		"EXT:\r\n"+
		"CACHE-CONTROL: max-age=100\r\n"+
		"LOCATION: http://%s:%d/description.xml\r\n"+
		"SERVER: Linux/3.14.0 UPnP/1.0 IpBridge/1.20.0\r\n"+
		"hue-bridgeid: %s\r\n"+
		"ST: urn:schemas-upnp-org:device:basic:1\r\n"+
		"USN: uuid:2f402f80-da50-11e1-9b23-%s\r\n\r\n",
		bridgeIP, port, bridgeID, macLower)

	_, _ = conn.Write([]byte(resp1))
	time.Sleep(10 * time.Millisecond)
	_, _ = conn.Write([]byte(resp2))
	time.Sleep(10 * time.Millisecond)
	_, _ = conn.Write([]byte(resp3))
}

func (d *DiscoveryManager) broadcastSSDPNotify(bridgeIP string, port int, bridgeID string, mac string) {
	addr, err := net.ResolveUDPAddr("udp4", "239.255.255.250:1900")
	if err != nil {
		slog.Error("SSDP Notify: Failed to resolve address", "error", err)
		return
	}

	macLower := strings.ToLower(mac)

	notifyMsg := fmt.Sprintf("NOTIFY * HTTP/1.1\r\n"+
		"HOST: 239.255.255.250:1900\r\n"+
		"CACHE-CONTROL: max-age=100\r\n"+
		"LOCATION: http://%s:%d/description.xml\r\n"+
		"SERVER: Linux/3.14.0 UPnP/1.0 IpBridge/1.20.0\r\n"+
		"NT: upnp:rootdevice\r\n"+
		"USN: uuid:2f402f80-da50-11e1-9b23-%s::upnp:rootdevice\r\n"+
		"NTS: ssdp:alive\r\n"+
		"hue-bridgeid: %s\r\n\r\n",
		bridgeIP, port, macLower, bridgeID)

	slog.Info("SSDP: Started active notifications", "interval", "60s")

	// Get all interfaces to send notifies
	interfaces, err := net.Interfaces()
	if err != nil {
		slog.Error("SSDP Notify: Failed to list interfaces", "error", err)
		return
	}

	sendToAll := func(msg string) {
		for _, iface := range interfaces {
			if iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagUp == 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil || len(addrs) == 0 {
				continue
			}

			var localIP net.IP
			for _, a := range addrs {
				if ipNet, ok := a.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
					if ipNet.IP.To4() != nil {
						localIP = ipNet.IP
						break
					}
				}
			}
			if localIP == nil {
				continue
			}

			localUDPAddr := &net.UDPAddr{IP: localIP}
			conn, err := net.DialUDP("udp4", localUDPAddr, addr)
			if err != nil {
				continue
			}
			_, _ = conn.Write([]byte(msg))
			_ = conn.Close()
		}
	}

	// Send first notification immediately
	sendToAll(notifyMsg)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sendToAll(notifyMsg)
		case <-d.stopChan:
			// Send SSDP byebye message before shutdown
			byebyeMsg := fmt.Sprintf("NOTIFY * HTTP/1.1\r\n"+
				"HOST: 239.255.255.250:1900\r\n"+
				"NT: upnp:rootdevice\r\n"+
				"USN: uuid:2f402f80-da50-11e1-9b23-%s::upnp:rootdevice\r\n"+
				"NTS: ssdp:byebye\r\n"+
				"hue-bridgeid: %s\r\n\r\n",
				macLower, bridgeID)
			sendToAll(byebyeMsg)
			return
		}
	}
}
