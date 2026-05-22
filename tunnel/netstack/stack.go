// Package netstack wires gVisor's userspace TCP/IP stack to a TUN device and
// exposes a per-stream accept callback that the rest of the tunnel hooks
// into.
//
// The stack:
//
//   - Speaks IPv6 only (matches the ADR's ULA-v6 decision).
//   - Owns a single TUN device on Linux (`/dev/net/tun`).
//   - Routes every IP inside the configured /48 to itself.
//   - Hands every accepted TCP connection to a Handler, which is
//     responsible for resolving the remote address back to a connection
//     name and bridging the TCP endpoint with a tunnel Stream.
//
// The TUN setup lives in stack_linux.go; this file is OS-agnostic.
package netstack

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// NICID is the single NIC the stack exposes. We don't multi-home.
const NICID tcpip.NICID = 1

// Handler is invoked for every accepted TCP connection inside the netstack.
// The handler is responsible for closing the conn when done.
//
// localAddr is the address the peer dialed (i.e. the virtual IP of a
// connection name). remotePort is the destination port. The handler uses
// these to look up the connection name and open a tunnel stream.
type Handler func(conn *gonet.TCPConn, localAddr netip.Addr, localPort uint16)

// AcceptFunc decides whether a TCP SYN should be answered (true) or
// rejected with a RST (false). It is consulted *before* gVisor completes
// the 3-way handshake, so rejected flows surface to the client as a
// clean ECONNREFUSED rather than an immediately-closed connection.
//
// Returning false also prevents Handler from being invoked. The function
// must be cheap: it runs on gVisor's TCP segment-dispatch path.
//
// AcceptFunc may be nil; in that case every SYN is accepted.
type AcceptFunc func(localAddr netip.Addr, localPort uint16) bool

// DNSHandler receives raw UDP datagrams arriving at the resolver's address
// (the netstack gateway IP, port 53). It returns the response bytes.
type DNSHandler func(query []byte, src netip.AddrPort) ([]byte, error)

// Options configures a Stack.
type Options struct {
	// Prefix is the per-session /48 the netstack will route locally.
	Prefix netip.Prefix

	// Gateway is the address the resolver listens on. Must be inside Prefix.
	Gateway netip.Addr

	// MTU for the TUN device. Default 1500.
	MTU uint32

	// DeviceName is the requested TUN device name (Linux). Empty means let
	// the kernel pick. Useful for tests that want a predictable name.
	DeviceName string

	// TCPHandler is invoked for every accepted TCP connection. Required.
	TCPHandler Handler

	// TCPAccept, if non-nil, is invoked for every inbound SYN before the
	// 3-way handshake. Returning false rejects the connection with a RST
	// and the SYN is never promoted to an endpoint. nil means accept all.
	TCPAccept AcceptFunc

	// DNSHandler answers UDP packets sent to Gateway:53. Required.
	DNSHandler DNSHandler
}

// Stack owns the gVisor stack, the TUN device, and the goroutines that
// shovel packets between them.
type Stack struct {
	opts Options
	stk  *stack.Stack

	// device is the OS link (TUN on Linux). The channel.Endpoint mediates
	// between gVisor and the device fd.
	device   tunDevice
	endpoint *channel.Endpoint

	// Cancelled when Close is called.
	ctx    context.Context
	cancel context.CancelFunc

	closeOnce sync.Once
	closeErr  error
}

// New brings up a stack and a TUN device wired together. The stack starts
// accepting traffic immediately; the TCP and DNS handlers may be invoked
// before New returns to the caller's goroutine.
func New(ctx context.Context, opts Options) (*Stack, error) {
	if opts.TCPHandler == nil {
		return nil, errors.New("netstack: TCPHandler is required")
	}
	if opts.DNSHandler == nil {
		return nil, errors.New("netstack: DNSHandler is required")
	}
	if !opts.Prefix.IsValid() || !opts.Prefix.Addr().Is6() {
		return nil, errors.New("netstack: Prefix must be a valid IPv6 prefix")
	}
	if !opts.Gateway.Is6() || !opts.Prefix.Contains(opts.Gateway) {
		return nil, errors.New("netstack: Gateway must be inside Prefix and IPv6")
	}
	if opts.MTU == 0 {
		opts.MTU = 1500
	}

	device, err := openTUN(opts.DeviceName, opts.MTU)
	if err != nil {
		return nil, fmt.Errorf("open TUN: %w", err)
	}

	ep := channel.New(512, opts.MTU, "")
	stk := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})

	if err := stk.CreateNIC(NICID, ep); err != nil {
		_ = device.Close()
		return nil, fmt.Errorf("netstack: CreateNIC: %s", err)
	}
	// Address the NIC with the gateway IP. gVisor delivers traffic for
	// every address in the routing table to this NIC, but it must own at
	// least one local address before it accepts incoming TCP.
	protoAddr := tcpip.ProtocolAddress{
		Protocol: ipv6.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   tcpip.AddrFromSlice(opts.Gateway.AsSlice()),
			PrefixLen: opts.Prefix.Bits(),
		},
	}
	if err := stk.AddProtocolAddress(NICID, protoAddr, stack.AddressProperties{}); err != nil {
		_ = device.Close()
		return nil, fmt.Errorf("netstack: AddProtocolAddress: %s", err)
	}
	// Make the NIC promiscuous on its prefix: any destination address inside
	// the /48 is delivered locally instead of being treated as forward
	// traffic. This is what lets `fd...:beef::1234` route to the netstack
	// without a per-address AddProtocolAddress call.
	if err := stk.SetSpoofing(NICID, true); err != nil {
		_ = device.Close()
		return nil, fmt.Errorf("netstack: SetSpoofing: %s", err)
	}
	if err := stk.SetPromiscuousMode(NICID, true); err != nil {
		_ = device.Close()
		return nil, fmt.Errorf("netstack: SetPromiscuousMode: %s", err)
	}
	// Route everything in the prefix locally.
	stk.SetRouteTable([]tcpip.Route{
		{
			Destination: header.IPv6EmptySubnet,
			NIC:         NICID,
		},
	})

	sctx, cancel := context.WithCancel(ctx)
	s := &Stack{
		opts:     opts,
		stk:      stk,
		device:   device,
		endpoint: ep,
		ctx:      sctx,
		cancel:   cancel,
	}

	// Wire packet flow.
	go s.deviceToStack()
	go s.stackToDevice()

	// Install the TCP forwarder.
	tcpFwd := tcp.NewForwarder(stk, 0 /* rcvWnd */, 1024 /* maxInFlight */, s.onTCP)
	stk.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)

	// UDP forwarder for the DNS resolver.
	go s.serveUDPDNS()

	return s, nil
}

// DeviceName returns the kernel-assigned TUN device name (e.g. "tun0").
// Callers use it to install host-level routing.
func (s *Stack) DeviceName() string {
	if s.device == nil {
		return ""
	}
	return s.device.Name()
}

// Close shuts the stack and the TUN device down. Idempotent.
func (s *Stack) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.endpoint.Close()
		s.stk.Close()
		if s.device != nil {
			s.closeErr = s.device.Close()
		}
	})
	return s.closeErr
}

// onTCP handles a single inbound TCP SYN. The flow is:
//
//  1. Decode the destination address (rejecting unparseable IPs with RST).
//  2. Consult TCPAccept (if set). If it returns false, send a RST so the
//     client sees an immediate ECONNREFUSED instead of a connection that
//     opens and instantly closes.
//  3. Otherwise CreateEndpoint completes the 3-way handshake and hands
//     the conn off to TCPHandler.
//
// We always call Complete exactly once on every path; failing to do so
// would leak the request entry in the forwarder's inFlight map.
func (s *Stack) onTCP(req *tcp.ForwarderRequest) {
	id := req.ID()

	localBytes := id.LocalAddress.AsSlice()
	localAddr, ok := netip.AddrFromSlice(localBytes)
	if !ok {
		req.Complete(true /* send RST */)
		return
	}

	if s.opts.TCPAccept != nil && !s.opts.TCPAccept(localAddr, id.LocalPort) {
		req.Complete(true /* send RST -> ECONNREFUSED on the client */)
		return
	}

	var wq waiter.Queue
	ep, ipErr := req.CreateEndpoint(&wq)
	if ipErr != nil {
		req.Complete(true /* send RST */)
		return
	}
	req.Complete(false)

	conn := gonet.NewTCPConn(&wq, ep)
	go s.opts.TCPHandler(conn, localAddr, id.LocalPort)
}

// serveUDPDNS listens on the gateway address port 53 and routes datagrams to
// the DNS handler.
func (s *Stack) serveUDPDNS() {
	addr := tcpip.FullAddress{
		NIC:  NICID,
		Addr: tcpip.AddrFromSlice(s.opts.Gateway.AsSlice()),
		Port: 53,
	}
	pc, err := gonet.DialUDP(s.stk, &addr, nil, ipv6.ProtocolNumber)
	if err != nil {
		// Without DNS the tunnel is useless, but we don't have a great way
		// to surface this back to New's caller post-construction. Log via
		// context cancellation cause.
		s.cancel()
		return
	}
	defer pc.Close()

	go func() {
		<-s.ctx.Done()
		_ = pc.Close()
	}()

	buf := make([]byte, 1500)
	for {
		n, raddr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		src, err := netip.ParseAddrPort(raddr.String())
		if err != nil {
			continue
		}
		query := append([]byte(nil), buf[:n]...)
		raddrCopy := raddr
		go func(query []byte, src netip.AddrPort) {
			resp, err := s.opts.DNSHandler(query, src)
			if err != nil || len(resp) == 0 {
				return
			}
			_, _ = pc.WriteTo(resp, raddrCopy)
		}(query, src)
	}
}

// deviceToStack reads packets from the TUN and injects them into gVisor.
func (s *Stack) deviceToStack() {
	mtu := int(s.opts.MTU)
	buf := make([]byte, mtu+64)
	for {
		n, err := s.device.Read(buf)
		if err != nil {
			s.cancel()
			return
		}
		if n == 0 {
			continue
		}
		view := buffer.MakeWithData(append([]byte(nil), buf[:n]...))
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
			Payload: view,
		})
		s.endpoint.InjectInbound(ipv6.ProtocolNumber, pkt)
		pkt.DecRef()
	}
}

// stackToDevice reads packets from gVisor and writes them to the TUN.
func (s *Stack) stackToDevice() {
	for {
		pkt := s.endpoint.ReadContext(s.ctx)
		if pkt == nil {
			return
		}
		view := pkt.ToView()
		bytes := view.AsSlice()
		_, _ = s.device.Write(bytes)
		view.Release()
		pkt.DecRef()
	}
}
