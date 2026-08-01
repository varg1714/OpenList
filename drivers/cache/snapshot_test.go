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
	c := CachedObj{
		ID:        "id1",
		Path:      "/dir/a.txt",
		Name:      "a.txt",
		Size:      10,
		Modified:  time.Unix(100, 0),
		Ctime:     time.Unix(50, 0),
		IsFolder:  false,
		HashInfo:  utils.NewHashInfoByMap(map[*utils.HashType]string{utils.SHA1: "abc"}),
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
	obj := fromCachedObj(CachedObj{Name: "x.txt", IsFolder: true})
	if _, ok := obj.(*model.Object); !ok {
		t.Fatalf("expected *model.Object, got %T", obj)
	}
	if !obj.IsDir() {
		t.Errorf("expected folder")
	}
}

func TestMarshalUnmarshal(t *testing.T) {
	snaps := []CachedObj{{Name: "a", Path: "/a"}, {Name: "b", IsFolder: true, Thumbnail: "t"}}
	data, err := marshalObjs(snaps)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	objs, err := unmarshalObjs(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("expected 2 objs, got %d", len(objs))
	}
	if objs[0].GetName() != "a" || !objs[1].IsDir() {
		t.Errorf("bad unmarshal: %+v %+v", objs[0], objs[1])
	}
}

func TestMarshalEmpty(t *testing.T) {
	data, err := marshalObjs(nil)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	objs, err := unmarshalObjs(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(objs) != 0 {
		t.Errorf("expected 0 objs, got %d", len(objs))
	}
}

func TestUnmarshalSpecialChars(t *testing.T) {
	data, err := marshalObjs([]CachedObj{{Name: strings.Repeat("很", 50) + ".txt"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	objs, err := unmarshalObjs(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if objs[0].GetName() != strings.Repeat("很", 50)+".txt" {
		t.Errorf("special chars corrupted")
	}
}
