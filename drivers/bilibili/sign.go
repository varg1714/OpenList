package bilibili

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 与 PiliPlus/BBDown 一致的前 32 位打乱索引（bilibili wbi 签名）
var mixinKeyEncTab = [32]int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35,
	27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13,
}

// getMixinKey 将 img_key+sub_key（64 字符）按打乱表映射为 32 字符 mixin key
func getMixinKey(orig string) string {
	b := make([]byte, 32)
	for i, idx := range mixinKeyEncTab {
		b[i] = orig[idx]
	}
	return string(b)
}

var wbiChrFilter = strings.NewReplacer("!", "", "'", "", "(", "", ")", "", "*", "")

// wbiEscape 同 url.QueryEscape，但空格编码为 %20（bilibili 要求）
func wbiEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// encWbi 注入 wts 并计算 w_rid，返回含 wts/w_rid 的新参数表
func encWbi(params map[string]string, mixinKey string) map[string]string {
	out := make(map[string]string, len(params)+2)
	for k, v := range params {
		out[k] = v
	}
	// 调用方不传 wts；测试可注入固定 wts 做向量验证
	if _, ok := out["wts"]; !ok {
		out["wts"] = strconv.FormatInt(time.Now().Unix(), 10)
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(wbiEscape(k))
		sb.WriteByte('=')
		sb.WriteString(wbiEscape(wbiChrFilter.Replace(out[k])))
	}
	sum := md5.Sum([]byte(sb.String() + mixinKey))
	out["w_rid"] = hex.EncodeToString(sum[:])
	return out
}
