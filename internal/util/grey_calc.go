package util

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
)

// GreyCalc 仅从uid中获取八位二进制进行计算
type GreyCalc struct {
	table map[uint32]uint8
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
	GreyCalcInstance = &GreyCalc{
		table: make(map[uint32]uint8, 10321920), // 8! * 256
	}
	notUsed := map[int]struct{}{0: {}, 1: {}, 2: {}, 3: {}, 4: {}, 5: {}, 6: {}, 7: {}}
	shuffleNums := make([]int, 0, 40320) // 8!
	allShuffleNumberGenerate(&notUsed, 0, 0, &shuffleNums)
	for _, shuffleNum := range shuffleNums {
		for uid := 0; uid < 256; uid++ {
			GreyCalcInstance.table[uint32(shuffleNum<<8|uid)] = greyCalc(uid, shuffleNum)
		}
	}
	return GreyCalcInstance
}

func allShuffleNumberGenerate(notUsed *map[int]struct{}, depth int, root int, ans *[]int) {
	if depth == 8 {
		*ans = append(*ans, root)
		return
	}
	notUsedCopy := make([]int, 0, 8)
	for k := range *notUsed {
		notUsedCopy = append(notUsedCopy, k)
	}
	srcRoot := root
	for i := 0; i < len(notUsedCopy); i++ {
		root = root<<3 | notUsedCopy[i]
		delete(*notUsed, notUsedCopy[i])
		allShuffleNumberGenerate(notUsed, depth+1, root, ans)
		root = srcRoot
		(*notUsed)[notUsedCopy[i]] = struct{}{}
	}
}
func shuffleNumberToArray(num int) []int {
	ans := make([]int, 8)
	for i := 0; i < 8; i++ {
		ans[7-i] = (num >> 3 * i) & 0x7
	}
	return ans
}

func greyCalc(uid int, shuffleNum int) uint8 {
	arr := shuffleNumberToArray(shuffleNum)
	var val uint8 = 0
	for i := 0; i < 8; i++ {
		val |= ((uint8(uid) >> arr[i]) & 1) << (7 - i)
	}
	return val
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
	key := shuffleNum<<8 | uint32(lastByte)
	val, found := g.table[key]
	if !found {
		return false, fmt.Errorf("grey calc table key not found: %d", key)
	}
	return float64(val)/256 < greyRate, nil
}
