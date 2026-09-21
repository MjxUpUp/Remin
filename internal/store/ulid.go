package store

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// NewULID：48 bit 毫秒时间戳 + 80 bit 随机，Crockford base32 编码 26 字符。
// 内容无关、时间单调、全局唯一（spec：id 永不复用）。
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var ulidNow = func() time.Time { return time.Now() }

func NewULID() (string, error) {
	var b [16]byte
	ms := uint64(ulidNow().UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		return "", fmt.Errorf("生成随机数失败: %w", err)
	}
	return encodeBase32(b), nil
}

// encodeBase32 大端 128 bit → 26 个 base32 字符（前置 2 个零位补齐 130 bit）
func encodeBase32(b [16]byte) string {
	var sb strings.Builder
	acc, nbits := 0, 0
	for i := 0; i < 16; i++ {
		acc = acc<<8 | int(b[i])
		nbits += 8
		for nbits >= 5 {
			nbits -= 5
			sb.WriteByte(crockford[(acc>>nbits)&31])
		}
	}
	if nbits > 0 {
		sb.WriteByte(crockford[(acc<<(5-nbits))&31])
	}
	return sb.String()
}

// NewMemoryID 记忆主键前缀 mem_
func NewMemoryID() (string, error) {
	u, err := NewULID()
	if err != nil {
		return "", err
	}
	return "mem_" + u, nil
}

// HomeDir 用户主目录（测试可注入）
var HomeDir = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// DefaultRoot 默认真源位置 ~/.remin/
func DefaultRoot() string {
	return filepath.Join(HomeDir(), ".remin")
}

// ResolveRoot 真源根解析优先级：--root > $REMIN_HOME > ~/.remin
func ResolveRoot(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("REMIN_HOME"); env != "" {
		return env
	}
	return DefaultRoot()
}
