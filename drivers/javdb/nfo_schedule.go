package javdb

import "github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"

var syncJavdbMediaNFOs = virtual_file.SyncMediaNFOs

func (d *Javdb) syncConfiguredNFOs() error {
	if d.RefreshNfo {
		return syncJavdbMediaNFOs(d.ID, DriverName, true)
	}
	if d.SyncNfo {
		return syncJavdbMediaNFOs(d.ID, DriverName, false)
	}
	return nil
}
