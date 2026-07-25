package fc2

import "github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"

var syncFC2MediaNFOs = virtual_file.SyncMediaNFOs

func (d *FC2) syncConfiguredNFOs() error {
	if d.RefreshNfo {
		return syncFC2MediaNFOs(d.ID, "fc2", true)
	}
	if d.SyncNfo {
		return syncFC2MediaNFOs(d.ID, "fc2", false)
	}
	return nil
}
