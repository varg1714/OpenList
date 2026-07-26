package javdb

import (
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/stretchr/testify/require"
)

func TestJavdbNFOFlagsPreferForcedRefreshWhenBothEnabled(t *testing.T) {
	oldSync := syncJavdbMediaNFOs
	t.Cleanup(func() { syncJavdbMediaNFOs = oldSync })
	var options []virtual_file.MediaNFOSyncOptions
	syncJavdbMediaNFOs = func(storageID uint, source string, option virtual_file.MediaNFOSyncOptions) error {
		require.Equal(t, uint(82), storageID)
		require.Equal(t, DriverName, source)
		options = append(options, option)
		return nil
	}

	driver := Javdb{Addition: Addition{SyncNfo: true, RefreshNfo: true}}
	driver.ID = 82
	err := driver.syncConfiguredNFOs()

	require.NoError(t, err)
	require.Equal(t, []virtual_file.MediaNFOSyncOptions{{Force: true, IncludeCode: true}}, options)
}
