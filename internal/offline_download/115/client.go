package _115

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"

	_115 "github.com/OpenListTeam/OpenList/v4/drivers/115"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	sdk115 "github.com/SheltonZhu/115driver/pkg/driver"
)

type Cloud115 struct {
	refreshTaskCache bool
	recoveredTasks   sync.Map
}

func (p *Cloud115) Name() string {
	return "115 Cloud"
}

func (p *Cloud115) Items() []model.SettingItem {
	return nil
}

func (p *Cloud115) Run(task *tool.DownloadTask) error {
	return errs.NotSupport
}

func (p *Cloud115) Init() (string, error) {
	p.refreshTaskCache = false
	return "ok", nil
}

func (p *Cloud115) IsReady() bool {
	tempDir := setting.GetStr(conf.Pan115TempDir)
	if tempDir == "" {
		return false
	}
	storage, _, err := op.GetStorageAndActualPath(tempDir)
	if err != nil {
		return false
	}
	if _, ok := storage.(*_115.Pan115); !ok {
		return false
	}
	return true
}

func (p *Cloud115) AddURL(args *tool.AddUrlArgs) (string, error) {
	// 添加新任务刷新缓存
	p.refreshTaskCache = true
	storage, actualPath, err := op.GetStorageAndActualPath(args.TempDir)
	if err != nil {
		return "", err
	}
	driver115, ok := storage.(*_115.Pan115)
	if !ok {
		return "", fmt.Errorf("unsupported storage driver for offline download, only 115 Cloud is supported")
	}

	ctx := context.Background()

	if err := op.MakeDir(ctx, storage, actualPath); err != nil {
		return "", err
	}

	parentDir, err := op.GetUnwrap(ctx, storage, actualPath)
	if err != nil {
		return "", err
	}

	hashs, err := driver115.OfflineDownload(ctx, []string{args.Url}, parentDir)
	if err != nil || len(hashs) < 1 {
		// 115 rejects a duplicate submission with errcode 10008 ("任务已存在").
		// The existing task is still the right one to poll, so reuse it instead
		// of treating an idempotent add as a playback failure.
		if task, recovered := recoverExistingOfflineTask(ctx, driver115, args.Url); recovered {
			utils.Log.Infof("reusing existing 115 offline task %s for %s", task.InfoHash, args.Url)
			p.storeRecoveredTask(task)
			return task.InfoHash, nil
		}
		if err != nil {
			return "", fmt.Errorf("failed to add offline download task: %w", err)
		}
		return "", errors.New("failed to add offline download task: 115 returned no task hash")
	}

	return hashs[0], nil
}

func (p *Cloud115) Remove(task *tool.DownloadTask) error {
	storage, _, err := op.GetStorageAndActualPath(task.TempDir)
	if err != nil {
		return err
	}
	driver115, ok := storage.(*_115.Pan115)
	if !ok {
		return fmt.Errorf("unsupported storage driver for offline download, only 115 Cloud is supported")
	}

	ctx := context.Background()
	if err := driver115.DeleteOfflineTasks(ctx, []string{task.GID}, false); err != nil {
		return err
	}
	return nil
}

func (p *Cloud115) Status(task *tool.DownloadTask) (*tool.Status, error) {
	if recovered, ok := p.recoveredTask(task.GID); ok {
		return offlineTaskStatus(recovered), nil
	}
	storage, _, err := op.GetStorageAndActualPath(task.TempDir)
	if err != nil {
		return nil, err
	}
	driver115, ok := storage.(*_115.Pan115)
	if !ok {
		return nil, fmt.Errorf("unsupported storage driver for offline download, only 115 Cloud is supported")
	}

	tasks, err := driver115.OfflineList(context.Background())
	if err != nil {
		return nil, err
	}

	for _, t := range tasks {
		if t.InfoHash == task.GID {
			return offlineTaskStatus(t), nil
		}
	}
	s := &tool.Status{
		Progress:  0,
		NewGID:    "",
		Completed: false,
		Status:    "the task has been deleted",
		Err:       nil,
	}
	s.Err = fmt.Errorf("the task has been deleted")
	return nil, nil
}

// offlineActualFileID returns the concrete file created by an offline task.
// For ed2k tasks 115 reports the save directory in FileId and the single
// downloaded file in DelFileId. FileId is still kept for listing the task
// directory; ActualFileId is only set when DelFileId adds new information.
func offlineActualFileID(fileID, delFileID string) string {
	if delFileID != "" && delFileID != fileID {
		return delFileID
	}
	return ""
}

// magnetInfoHash extracts the BitTorrent info hash from a magnet URI as
// lowercase hex. Both the 40-character hex and 32-character base32 forms of
// the btih parameter are accepted.
func magnetInfoHash(magnetURI string) (string, bool) {
	parsed, err := url.Parse(magnetURI)
	if err != nil || !strings.EqualFold(parsed.Scheme, "magnet") {
		return "", false
	}
	const prefix = "urn:btih:"
	for _, xt := range parsed.Query()["xt"] {
		if !strings.HasPrefix(strings.ToLower(xt), prefix) {
			continue
		}
		raw := xt[len(prefix):]
		switch len(raw) {
		case 40:
			decoded, decodeErr := hex.DecodeString(raw)
			if decodeErr == nil && len(decoded) == 20 {
				return hex.EncodeToString(decoded), true
			}
		case 32:
			decoded, decodeErr := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(raw))
			if decodeErr == nil && len(decoded) == 20 {
				return hex.EncodeToString(decoded), true
			}
		}
	}
	return "", false
}

func findOfflineTask(infoHash string, tasks []*sdk115.OfflineTask) (*sdk115.OfflineTask, bool) {
	if infoHash == "" {
		return nil, false
	}
	for _, task := range tasks {
		if task != nil && strings.EqualFold(task.InfoHash, infoHash) {
			return task, true
		}
	}
	return nil, false
}

func findOfflineTaskHash(infoHash string, tasks []*sdk115.OfflineTask) (string, bool) {
	task, ok := findOfflineTask(infoHash, tasks)
	if !ok {
		return "", false
	}
	return task.InfoHash, true
}

// ed2kInfoHash extracts the ed2k file hash from an ed2k URI as lowercase hex.
func ed2kInfoHash(ed2kURI string) (string, bool) {
	parts := strings.Split(ed2kURI, "|")
	if len(parts) < 6 || !strings.EqualFold(parts[0], "ed2k://") || !strings.EqualFold(parts[1], "file") {
		return "", false
	}
	raw := parts[4]
	if len(raw) != 32 {
		return "", false
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != 16 {
		return "", false
	}
	return hex.EncodeToString(decoded), true
}

func linkInfoHash(link string) (string, bool) {
	if hash, ok := magnetInfoHash(link); ok {
		return hash, true
	}
	return ed2kInfoHash(link)
}

// maxOfflineTaskPages bounds the duplicate-task search so a broken pagination
// cursor can never turn playback into an unbounded request loop.
const maxOfflineTaskPages = 20

var listOfflineTasksPage = func(ctx context.Context, client *_115.Pan115, page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
	return client.OfflineListPage(ctx, page)
}

// recoverExistingOfflineTask reuses the offline task 115 already holds for the
// same magnet or ed2k link. Submitting a duplicate returns errcode 10008
// ("任务已存在"), which the driver surfaces as a generic error, but the existing
// task is still the correct one to poll.
func recoverExistingOfflineTask(ctx context.Context, client *_115.Pan115, link string) (*sdk115.OfflineTask, bool) {
	infoHash, ok := linkInfoHash(link)
	if !ok {
		return nil, false
	}
	return findOfflineTaskAcrossPages(infoHash, func(page int64) ([]*sdk115.OfflineTask, int64, bool, error) {
		return listOfflineTasksPage(ctx, client, page)
	})
}

// findOfflineTaskAcrossPages walks the paginated offline task list until it
// finds the info hash. The provider echoes the page it served, so the cursor is
// taken from the response rather than assumed to be 0- or 1-based.
func findOfflineTaskAcrossPages(infoHash string, fetch func(page int64) ([]*sdk115.OfflineTask, int64, bool, error)) (*sdk115.OfflineTask, bool) {
	page := int64(0)
	for requests := 0; requests < maxOfflineTaskPages; requests++ {
		tasks, next, more, err := fetch(page)
		if err != nil || len(tasks) == 0 {
			return nil, false
		}
		if task, ok := findOfflineTask(infoHash, tasks); ok {
			return task, true
		}
		if !more || next <= page {
			return nil, false
		}
		page = next
	}
	return nil, false
}

// recoveredTaskTTL bounds how long a reused task snapshot may answer status
// polls. A snapshot is only a shortcut for the playback that reused it.
const recoveredTaskTTL = 5 * time.Minute

type recoveredTaskSnapshot struct {
	task *sdk115.OfflineTask
	at   time.Time
}

func (p *Cloud115) storeRecoveredTask(task *sdk115.OfflineTask) {
	if task == nil || task.InfoHash == "" {
		return
	}
	p.recoveredTasks.Store(task.InfoHash, recoveredTaskSnapshot{task: task, at: time.Now()})
}

// recoveredTask returns a reused task only when the snapshot can already answer
// the status poll. In-progress snapshots are stale and must not mask the live
// task list, or the poll would loop until the download timeout.
func (p *Cloud115) recoveredTask(gid string) (*sdk115.OfflineTask, bool) {
	value, ok := p.recoveredTasks.Load(gid)
	if !ok {
		return nil, false
	}
	snapshot, ok := value.(recoveredTaskSnapshot)
	if !ok || snapshot.task == nil || time.Since(snapshot.at) > recoveredTaskTTL {
		p.recoveredTasks.Delete(gid)
		return nil, false
	}
	if !snapshot.task.IsDone() && !snapshot.task.IsFailed() {
		return nil, false
	}
	return snapshot.task, true
}

func offlineTaskStatus(t *sdk115.OfflineTask) *tool.Status {
	s := &tool.Status{
		Progress:   t.Percent,
		Completed:  t.IsDone(),
		Status:     t.GetStatus(),
		TotalBytes: t.Size,
		FileInfo: tool.FileInfo{
			FileId:       t.FileId,
			ActualFileId: offlineActualFileID(t.FileId, t.DelFileId),
		},
	}
	if t.IsFailed() {
		s.Err = errors.New(t.GetStatus())
	}
	return s
}

var _ tool.Tool = (*Cloud115)(nil)

func init() {
	tool.Tools.Add(&Cloud115{})
}
