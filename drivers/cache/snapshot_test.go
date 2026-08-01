package cache

import (
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func TestToCachedObjPlain(t *testing.T) {
	obj := &model.Object{
		ID:       "id1",
		Name:     "a.txt",
		Size:     10,
		Modified: time.Unix(100, 0),
		IsFolder: false,
	}
	c := toCachedObj("/dir", obj)
	if c.Path != "/dir/a.txt" {
		t.Errorf("expected path /dir/a.txt, got %s", c.Path)
	}
	if c.Name != "a.txt" || c.Size != 10 || c.IsFolder {
		t.Errorf("bad snapshot: %+v", c)
	}
	if c.Thumbnail != "" {
		t.Errorf("expected empty thumbnail, got %s", c.Thumbnail)
	}
}

func TestToCachedObjWithThumb(t *testing.T) {
	obj := &model.ObjThumb{
		Object:    model.Object{Name: "b.jpg", Size: 5},
		Thumbnail: model.Thumbnail{Thumbnail: "https://example.com/thumb.jpg"},
	}
	c := toCachedObj("/", obj)
	if c.Thumbnail != "https://example.com/thumb.jpg" {
		t.Errorf("expected thumbnail, got %s", c.Thumbnail)
	}
}

func TestFromCachedObjRoundTrip(t *testing.T) {
	c := model.CachedObj{
		ID:        "id1",
		Path:      "/dir/a.txt",
		Name:      "a.txt",
		Size:      10,
		Modified:  time.Unix(100, 0),
		Ctime:     time.Unix(50, 0),
		IsFolder:  false,
		HashInfo:  map[string]string{"sha1": "abc"},
		Thumbnail: "https://example.com/thumb.jpg",
	}
	obj := fromCachedObj(c)
	if _, ok := obj.(*model.ObjThumb); !ok {
		t.Fatalf("expected *model.ObjThumb, got %T", obj)
	}
	if obj.GetName() != "a.txt" || obj.GetSize() != 10 || obj.GetPath() != "/dir/a.txt" {
		t.Errorf("round trip mismatch: %+v", obj)
	}
	if obj.GetHash().GetHash(utils.SHA1) != "abc" {
		t.Errorf("hash mismatch")
	}
	if obj.CreateTime().Unix() != 50 {
		t.Errorf("ctime mismatch")
	}
}

func TestFromCachedObjNoThumb(t *testing.T) {
	obj := fromCachedObj(model.CachedObj{Name: "x.txt", IsFolder: true})
	if _, ok := obj.(*model.Object); !ok {
		t.Fatalf("expected *model.Object, got %T", obj)
	}
	if !obj.IsDir() {
		t.Errorf("expected folder")
	}
}

func TestHashRoundTrip(t *testing.T) {
	obj := &model.Object{
		Name:     "h.txt",
		HashInfo: utils.NewHashInfoByMap(map[*utils.HashType]string{utils.SHA1: "abc", utils.MD5: "def"}),
	}
	c := toCachedObj("/", obj)
	if c.HashInfo["sha1"] != "abc" || c.HashInfo["md5"] != "def" {
		t.Errorf("hash not exported: %+v", c.HashInfo)
	}
	obj2 := fromCachedObj(c)
	if obj2.GetHash().GetHash(utils.SHA1) != "abc" || obj2.GetHash().GetHash(utils.MD5) != "def" {
		t.Errorf("hash not restored: %s %s", obj2.GetHash().GetHash(utils.SHA1), obj2.GetHash().GetHash(utils.MD5))
	}
}

func TestSpecialCharsName(t *testing.T) {
	name := strings.Repeat("很", 50) + ".txt"
	c := toCachedObj("/", &model.Object{Name: name})
	if c.Name != name {
		t.Errorf("special chars corrupted: %q", c.Name)
	}
}
