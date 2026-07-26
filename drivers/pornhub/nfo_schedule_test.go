package pornhub

import (
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/stretchr/testify/require"
)

func TestPornhubNFOFlagsPreferForcedRefreshWhenBothEnabled(t *testing.T) {
	oldSync := syncPornhubMediaNFOs
	t.Cleanup(func() { syncPornhubMediaNFOs = oldSync })
	var options []virtual_file.MediaNFOSyncOptions
	syncPornhubMediaNFOs = func(storageID uint, source string, option virtual_file.MediaNFOSyncOptions) error {
		require.Equal(t, uint(83), storageID)
		require.Equal(t, DriverName, source)
		options = append(options, option)
		return nil
	}

	driver := Pornhub{Addition: Addition{SyncNfo: true, RefreshNfo: true}}
	driver.ID = 83
	err := driver.syncConfiguredNFOs()

	require.NoError(t, err)
	require.Equal(t, []virtual_file.MediaNFOSyncOptions{{Force: true}}, options)
}
