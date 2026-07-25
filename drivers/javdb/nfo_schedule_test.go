package javdb

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJavdbNFOFlagsPreferForcedRefreshWhenBothEnabled(t *testing.T) {
	oldSync := syncJavdbMediaNFOs
	t.Cleanup(func() { syncJavdbMediaNFOs = oldSync })
	var forces []bool
	syncJavdbMediaNFOs = func(storageID uint, source string, force bool) error {
		require.Equal(t, uint(82), storageID)
		require.Equal(t, DriverName, source)
		forces = append(forces, force)
		return nil
	}

	driver := Javdb{Addition: Addition{SyncNfo: true, RefreshNfo: true}}
	driver.ID = 82
	err := driver.syncConfiguredNFOs()

	require.NoError(t, err)
	require.Equal(t, []bool{true}, forces)
}
