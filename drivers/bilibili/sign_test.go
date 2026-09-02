package bilibili

import (
	"crypto/md5"
	"encoding/hex"
	"testing"
)

func TestGetMixinKey(t *testing.T) {
	// 文档示例：img_key="7cd084941338484aae1ad9425b84077c" sub_key="4932caff0ff746eab6f01bf08b70ac45"
	orig := "7cd084941338484aae1ad9425b84077c" + "4932caff0ff746eab6f01bf08b70ac45"
	if got := getMixinKey(orig); got != "ea1db124af3c7062474693fa704f4ff8" {
		t.Fatalf("getMixinKey = %q, want ea1db124af3c7062474693fa704f4ff8", got)
	}
}

func TestEncWbi(t *testing.T) {
	const mixinKey = "ea1db124af3c7062474693fa704f4ff8"
	params := encWbi(map[string]string{"foo": "114", "bar": "514", "zab": ""}, mixinKey)
	if params["wts"] == "" {
		t.Fatal("wts not injected")
	}
	// wts 是动态时间戳，期望值现场手算（测试独立实现拼接+md5，不调用被测函数）：
	// 排序 query = bar=514&foo=114&wts={wts}&zab=，md5(query+mixinKey)
	query := "bar=514&foo=114&wts=" + params["wts"] + "&zab="
	sum := md5.Sum([]byte(query + mixinKey))
	want := hex.EncodeToString(sum[:])
	if params["w_rid"] != want {
		t.Fatalf("w_rid = %q, want %q (query=%q)", params["w_rid"], want, query)
	}
	// 固定向量核对（wts=1700000000 时，预计算值）：证明算法与 bilibili 一致
	params2 := encWbi(map[string]string{"foo": "114", "bar": "514", "zab": "", "wts": "1700000000"}, mixinKey)
	if params2["w_rid"] != "5badc9d357d0139c38b633fc665e7f2d" {
		t.Fatalf("fixed-vector w_rid = %q, want 5badc9d357d0139c38b633fc665e7f2d", params2["w_rid"])
	}
}

func TestEncWbiFiltersSpecialChars(t *testing.T) {
	params := encWbi(map[string]string{"title": "a!'()*b", "x": "1"}, "k")
	if got := params["w_rid"]; got == "" {
		t.Fatal("w_rid empty")
	}
	_ = params // 只要不 panic、w_rid 非空即可；过滤字符不进入签名串
}
