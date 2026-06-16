// Minimal Minecraft server-list ping (status). stdlib only.
// ponytail: just enough protocol to confirm the proxy answers; swap for mcstatus if richer checks are ever needed.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"time"
)

func writeVarInt(b *bytes.Buffer, v int) {
	uv := uint32(v)
	for {
		if uv&^0x7f == 0 {
			b.WriteByte(byte(uv))
			return
		}
		b.WriteByte(byte(uv&0x7f | 0x80))
		uv >>= 7
	}
}

func packet(id byte, payload []byte) []byte {
	var body bytes.Buffer
	body.WriteByte(id)
	body.Write(payload)
	var out bytes.Buffer
	writeVarInt(&out, body.Len())
	out.Write(body.Bytes())
	return out.Bytes()
}

func mustPort(s string) int {
	var p int
	fmt.Sscanf(s, "%d", &p)
	return p
}

func main() {
	if len(os.Args) != 3 {
		fmt.Println("usage: smoke <host> <port>")
		os.Exit(2)
	}
	host, port := os.Args[1], os.Args[2]
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 5*time.Second)
	if err != nil {
		fmt.Println("dial:", err)
		os.Exit(1)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	var hs bytes.Buffer
	writeVarInt(&hs, 766) // protocol version (any recent)
	writeVarInt(&hs, len(host))
	hs.WriteString(host)
	binary.Write(&hs, binary.BigEndian, uint16(mustPort(port)))
	writeVarInt(&hs, 1) // next state = status
	conn.Write(packet(0x00, hs.Bytes()))
	conn.Write(packet(0x00, nil)) // status request

	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err != nil {
		fmt.Println("no status response:", err)
		os.Exit(1)
	}
	fmt.Println("OK: proxy answered status ping")
}
