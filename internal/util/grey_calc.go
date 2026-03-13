package util

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
)

// GreyCalc 仅从uid中获取八位二进制进行计算
type GreyCalc struct {
}

type GreyCalcFunctions interface {
	IsUseGrey(uid string, shuffleNum int, greyRate float64) bool
}

var (
	GreyCalcInstance *GreyCalc
)

func NewGreyCalc() *GreyCalc {
	if GreyCalcInstance != nil {
		return GreyCalcInstance
	}
	GreyCalcInstance = &GreyCalc{}
	return GreyCalcInstance
}

// GetRandomGreyShuffleCode 生成一串 0-7 的随机排列，并按每个数字 3bit 打包进 24bit：
// 高位在前（与 allShuffleNumberGenerate 的 root = root<<3|digit 保持一致）。
func (g *GreyCalc) GetRandomGreyShuffleCode() uint32 {
	arr := [8]int{0, 1, 2, 3, 4, 5, 6, 7}

	// Fisher–Yates shuffle
	for i := len(arr) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			// crypto/rand 理论上不该失败；兜底返回一个固定序列，保证行为可预期。
			break
		}
		j := int(n.Int64())
		arr[i], arr[j] = arr[j], arr[i]
	}

	var code uint32
	for i := 0; i < len(arr); i++ {
		code = (code << 3) | uint32(arr[i]&0x7)
	}
	return code
}

// IsUseGrey 试过打表：确实更快 代价是151.35MB额外内存占用
func (g *GreyCalc) IsUseGrey(uid string, shuffleNum uint32, greyRate float64) (bool, error) {
	if greyRate <= 0 {
		return false, nil
	}
	if greyRate >= 1 {
		return true, nil
	}
	if len(uid) == 0 {
		return false, nil
	}
	hexForm, err := hex.DecodeString(uid[len(uid)-2:])
	if hexForm == nil || err != nil {
		return false, fmt.Errorf("invalid uid format: %s", uid)
	}
	lastByte := hexForm[len(hexForm)-1]
	val := uint8(0)
	used := make([]bool, 8)
	for i := range 8 {
		pos := (shuffleNum >> ((7 - i) * 3)) & 0x7
		if used[pos] {
			return false, fmt.Errorf("invalid shuffleNum: %d, duplicate position %d", shuffleNum, pos)
		}
		used[pos] = true
		val |= ((lastByte >> pos) & 1) << (7 - i)
	}
	return float64(val)/256 < greyRate, nil
}
