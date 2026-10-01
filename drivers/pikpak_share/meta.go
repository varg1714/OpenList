package pikpak_share

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	driver.RootID
	ShareId               string `json:"share_id" required:"true"`
	SharePwd              string `json:"share_pwd"`
	Platform              string `json:"platform" default:"web" required:"true" type:"select" options:"android,web,pc"`
	DeviceID              string `json:"device_id"  required:"false" default:""`
	UseTransCodingAddress bool   `json:"use_transcoding_address" required:"true" default:"false"`
	PikPakDriverPath      string `json:"pik_pak_driver_path" required:"true" default:"" type:"text"`
	TransformFileSize     int64  `json:"transform_file_size" required:"true" default:"0" type:"number"`
	LinkParse             string `json:"link_parse" type:"text" help:"regex to parse share link, named capture group names must match field names, e.g. (?<shareId>...)"`
}

var defaultLinkParsePattern = `^https?://(?:[A-Za-z0-9-]+\.)*mypikpak\.(?:com|net)/s/(?<shareId>[A-Za-z0-9_-]+)`

var config = driver.Config{
	Name:      "PikPakShare",
	LocalSort: true,
	NoUpload:  true,
	LinkParse: &driver.LinkParseConfig{Pattern: defaultLinkParsePattern},
}

func (d *PikPakShare) Config() driver.Config {
	cfg := config
	if d.Addition.LinkParse != "" {
		cfg.LinkParse = &driver.LinkParseConfig{Pattern: d.Addition.LinkParse}
	}
	return cfg
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &PikPakShare{}
	})
}
