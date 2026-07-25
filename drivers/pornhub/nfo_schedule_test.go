package pornhub

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPornhubNFOFlagsPreferForcedRefreshWhenBothEnabled(t *testing.T) {
	oldSync := syncPornhubMediaNFOs
	t.Cleanup(func() { syncPornhubMediaNFOs = oldSync })
	var forces []bool
	syncPornhubMediaNFOs = func(storageID uint, source string, force bool) error {
		require.Equal(t, uint(83), storageID)
		require.Equal(t, DriverName, source)
		forces = append(forces, force)
		return nil
	}

	driver := Pornhub{Addition: Addition{SyncNfo: true, RefreshNfo: true}}
	driver.ID = 83
	err := driver.syncConfiguredNFOs()

	require.NoError(t, err)
	require.Equal(t, []bool{true}, forces)
}
