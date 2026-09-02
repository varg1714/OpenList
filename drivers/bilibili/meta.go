package bilibili

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	// TODO(Task 6): import op and re-enable init() below once
	// Bilibili implements the full driver.Driver interface (List/Link).
	// "github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	Cookie       string `json:"cookie" type:"text" help:"auto-filled after QR code login; or paste browser cookie manually (must contain SESSDATA)"`
	MaxListItems int    `json:"max_list_items" type:"number" default:"500" help:"max items per paged list (followings/videos/fav), 0 = unlimited"`
	driver.RootPath
}

var config = driver.Config{
	Name:        "Bilibili",
	LocalSort:   false, // driver returns pubdate-desc order; keep as-is
	NoUpload:    true,
	DefaultRoot: "/",
}

type Bilibili struct {
	model.Storage
	Addition
}

func (d *Bilibili) Config() driver.Config {
	return config
}

func (d *Bilibili) GetAddition() driver.Additional {
	return &d.Addition
}

// TODO(Task 6): enable registration once Bilibili implements driver.Driver.
//
// func init() {
// 	op.RegisterDriver(func() driver.Driver {
// 		return &Bilibili{}
// 	})
// }
