package emby_wrapper

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	RemotePath      string `json:"remote_path" required:"true" help:"the mount path of the downstream storage"`
	FilterFileTypes string `json:"filter_file_types" type:"text" default:"mp4,mkv,flv,avi,wmv,ts,rmvb,webm,mp3,flac,aac,wav,ogg,m4a,wma,alac" required:"false" help:"file extensions that get a virtual nfo"`
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
