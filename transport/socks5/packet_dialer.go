// Copyright 2024 Jigsaw Operations LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package socks5

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/Jigsaw-Code/outline-sdk/transport"
)

type packetConn struct {
	dstAddr net.Addr
	pc      net.Conn
	sc      transport.StreamConn
}

var _ net.Conn = (*packetConn)(nil)

func (c *packetConn) LocalAddr() net.Addr {
	// TODO: Is this right?
	return c.pc.LocalAddr()
}

func (c *packetConn) RemoteAddr() net.Addr {
	return c.dstAddr
}

func (c *packetConn) SetDeadline(t time.Time) error {
	return c.pc.SetDeadline(t)
}

func (c *packetConn) SetReadDeadline(t time.Time) error {
	return c.pc.SetReadDeadline(t)
}

func (c *packetConn) SetWriteDeadline(t time.Time) error {
	return c.pc.SetWriteDeadline(t)
}

func (c *packetConn) Read(b []byte) (int, error) {
	// TODO: read header
	buffer := make([]byte, 65536) // Maximum size for UDP packet
	n, err := c.pc.Read(buffer)
	if err != nil {
		return 0, err
	}
	// Minimum size of header is 10 bytes
	if n < 10 {
		return 0, fmt.Errorf("invalid SOCKS5 UDP packet: too short")
	}

	// Start parsing the header
	rsv := buffer[:2]
	if rsv[0] != 0x00 || rsv[1] != 0x00 {
		return 0, fmt.Errorf("invalid reserved bytes: expected 0x0000, got %#x%#x", rsv[0], rsv[1])
	}

	frag := buffer[2]
	if frag != 0 {
		return 0, fmt.Errorf("fragmentation is not supported")
	}

	atyp := buffer[3]
	addrLen := 0
	switch atyp {
	case addrTypeIPv4:
		addrLen = net.IPv4len
	case addrTypeIPv6:
		addrLen = net.IPv6len
	case addrTypeDomainName:
		// Domain name's first byte is the length of the name
		addrLen = int(buffer[4]) + 1 // +1 for the length byte itself
	default:
		return 0, fmt.Errorf("unknown address type %#x", atyp)
	}

	// Calculate the start position of the actual data
	headerLength := 4 + addrLen + 2 // RSV (2) + FRAG (1) + ATYP (1) + ADDR (variable) + PORT (2)
	if n < headerLength {
		return 0, fmt.Errorf("invalid SOCKS5 UDP packet: header too short")
	}

	// Copy the payload into the provided buffer
	payloadLength := n - headerLength
	if payloadLength > len(b) {
		// maybe raise an error to indicate that the provided buffer is too small?
		payloadLength = len(b)
	}
	copy(b, buffer[headerLength:n])

	return payloadLength, nil
}

func (c *packetConn) Write(b []byte) (int, error) {
	// Encapsulate the payload in a SOCKS5 UDP packet
	header := []byte{
		0x00, 0x00, // Reserved
		0x00, // Fragment number
		// To be appended below: ATYP, IPv4, IPv6, Domain name
		// To be appended below: IP and port (destination address)
	}
	header, err := appendSOCKS5Address(header, c.dstAddr.String())
	if err != nil {
		return 0, fmt.Errorf("failed to append SOCKS5 address: %w", err)
	}
	// Combine the header and the payload
	fullPacket := append(header, b...)
	return c.pc.Write(fullPacket)
}

func (c *packetConn) Close() error {
	return errors.Join(c.sc.Close(), c.pc.Close())
}

// DialPacket creates a packet [net.Conn] via SOCKS5.
func (d *Dialer) DialPacket(ctx context.Context, dstAddr string) (net.Conn, error) {
	netDstAddr, err := transport.MakeNetAddr("udp", dstAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse address: %w", err)
	}
	// TODO: how to provide the bind address?
	sc, bindAddr, err := d.request(ctx, CmdUDPAssociate, "0.0.0.0:0")
	//fmt.Println("Bound address is:", bindAddr)
	if err != nil {
		return nil, err
	}

	host, port, err := net.SplitHostPort(bindAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse bound address: %w", err)
	}
	fmt.Printf("bound host is %v, bound port is %v \n", host, port)

	if host == "::" {
		schost, scPort, err := net.SplitHostPort(sc.RemoteAddr().String())
		if err != nil {
			return nil, fmt.Errorf("failed to parse tcp address: %w", err)
		}
		fmt.Printf("tcp host is %v, tcp port is %v \n", schost, scPort)
		host = schost
	}

	pc, err := d.pd.DialPacket(ctx, net.JoinHostPort(host, port))
	if err != nil {
		sc.Close()
		return nil, fmt.Errorf("failed to connect to packet endpoint: %w", err)
	}

	return &packetConn{netDstAddr, pc, sc}, nil
}
