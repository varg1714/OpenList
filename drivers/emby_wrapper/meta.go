package emby_wrapper

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	RemotePath      string `json:"remote_path" required:"true" help:"the mount path of the downstream storage"`
	FilterFileTypes string `json:"filter_file_types" type:"text" default:"mp4,mkv,flv,avi,wmv,ts,rmvb,webm,mp3,flac,aac,wav,ogg,m4a,wma,alac" required:"false" help:"file extensions that get a virtual nfo"`
	// FanartCount: 电视剧目录多图（对齐 pornhub fanart 机制）：剧集根自动附加
	// poster.jpg（主海报）+ fanart1..N.jpg（背景），取剧集顺序前 N 个带缩略图的视频；
	// 真实同名文件优先；0 关闭（存量存储需自行开启）
	FanartCount int `json:"fanart_count" type:"number" default:"10" required:"false" help:"show root multi-image count (poster.jpg + fanart1..N.jpg from first N episode thumbs); 0 = off"`
}

var config = driver.Config{
	Name:        "EmbyWrapper",
	LocalSort:   true,
	NoUpload:    true,
	DefaultRoot: "/",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &EmbyWrapper{}
	})
}
