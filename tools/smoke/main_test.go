package main

import (
	"bytes"
	"testing"
)

func TestVarInt(t *testing.T) {
	cases := map[int][]byte{0: {0x00}, 1: {0x01}, 127: {0x7f}, 128: {0x80, 0x01}, 300: {0xac, 0x02}}
	for in, want := range cases {
		var b bytes.Buffer
		writeVarInt(&b, in)
		if !bytes.Equal(b.Bytes(), want) {
			t.Errorf("varint(%d)=%x want %x", in, b.Bytes(), want)
		}
	}
}
