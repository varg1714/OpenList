package strm

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	Paths                string `json:"paths" required:"true" type:"text"`
	SiteUrl              string `json:"siteUrl" type:"text" required:"false" help:"The prefix URL of the strm file"`
	FilterFileTypes      string `json:"filterFileTypes" type:"text" default:"strm" required:"false" help:"Supports suffix name of strm file"`
	DownloadFileTypes    string `json:"downloadFileTypes" type:"text" default:"ass" required:"false" help:"Files need to download with strm (usally subtitles)"`
	EncodePath           bool   `json:"encodePath" default:"true" required:"true" help:"encode the path in the strm file"`
	WithoutUrl           bool   `json:"withoutUrl" default:"false" help:"strm file content without URL prefix"`
	SaveStrmToLocal      bool   `json:"SaveStrmToLocal" default:"false" help:"save strm file locally"`
	SaveStrmLocalPath    string `json:"SaveStrmLocalPath" type:"text" help:"save strm file local path"`
	DeleteExtraLocalFile bool   `json:"deleteExtraLocalFile" default:"false" help:"delete extra file locally"`
}

var config = driver.Config{
	Name:          "Strm",
	LocalSort:     true,
	NoCache:       false,
	NoUpload:      true,
	DefaultRoot:   "/",
	OnlyLinkMFile: true,
	OnlyProxy:     true,
	NoLinkURL:     true,
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Strm{
			Addition: Addition{
				EncodePath: true,
			},
		}
	})
}
