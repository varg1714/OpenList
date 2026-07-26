package pornhub

import "github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"

var syncPornhubMediaNFOs = virtual_file.SyncMediaNFOs

func (d *Pornhub) syncConfiguredNFOs() error {
	if d.RefreshNfo {
		return syncPornhubMediaNFOs(d.ID, DriverName, virtual_file.MediaNFOSyncOptions{Force: true})
	}
	if d.SyncNfo {
		return syncPornhubMediaNFOs(d.ID, DriverName, virtual_file.MediaNFOSyncOptions{})
	}
	return nil
}
