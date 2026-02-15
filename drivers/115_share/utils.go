package _115_share

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_115 "github.com/OpenListTeam/OpenList/v4/drivers/115"
	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/drivers/virtual_file"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	driver115 "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/pkg/errors"
)

var _ model.Obj = (*FileObj)(nil)

type FileObj struct {
	Size        int64
	Sha1        string
	Utm         time.Time
	FileName    string
	isDir       bool
	FileID      string
	Path        string
	ThumbURL    string
	RawFileName string
}

func (f *FileObj) CreateTime() time.Time {
	return f.Utm
}

func (f *FileObj) GetHash() utils.HashInfo {
	return utils.NewHashInfo(utils.SHA1, f.Sha1)
}

func (f *FileObj) GetSize() int64 {
	return f.Size
}

func (f *FileObj) GetName() string {
	return f.FileName
}

func (f *FileObj) ModTime() time.Time {
	return f.Utm
}

func (f *FileObj) IsDir() bool {
	return f.isDir
}

func (f *FileObj) GetID() string {
	return f.FileID
}

func (f *FileObj) GetPath() string {
	return f.Path
}

func (f *FileObj) SetName(newName string) {
	f.FileName = newName
}

func (f *FileObj) Thumb() string {
	return f.ThumbURL
}

func transFunc(parent model.Obj, sf driver115.ShareFile) (model.Obj, error) {
	timeInt, err := strconv.ParseInt(sf.UpdateTime, 10, 64)
	if err != nil {
		return nil, err
	}
	var (
		utm    = time.Unix(timeInt, 0)
		isDir  = (sf.IsFile == 0)
		fileID = string(sf.FileID)
	)
	if isDir {
		fileID = string(sf.CategoryID)
	}
	return &FileObj{
		Size:        int64(sf.Size),
		Sha1:        sf.Sha1,
		Utm:         utm,
		FileName:    string(sf.FileName),
		RawFileName: sf.FileName,
		isDir:       isDir,
		FileID:      fileID,
		Path:        filepath.Join(parent.GetPath(), fileID),
		ThumbURL:    sf.ThumbURL,
	}, nil
}

func (d *Pan115Share) login() error {
	var err error
	opts := []driver115.Option{
		driver115.UA(base.UserAgentNT),
	}
	d.client = driver115.New(opts...)
	cr := &driver115.Credential{}
	if d.QRCodeToken != "" {
		s := &driver115.QRCodeSession{
			UID: d.QRCodeToken,
		}
		if cr, err = d.client.QRCodeLoginWithApp(s, driver115.LoginApp(d.QRCodeSource)); err != nil {
			return errors.Wrap(err, "failed to login by qrcode")
		}
		d.Cookie = fmt.Sprintf("UID=%s;CID=%s;SEID=%s;KID=%s", cr.UID, cr.CID, cr.SEID, cr.KID)
		d.QRCodeToken = ""
	} else if d.Cookie != "" {
		if err = cr.FromCookie(d.Cookie); err != nil {
			return errors.Wrap(err, "failed to login by cookies")
		}
		d.client.ImportCredential(cr)
	} else {
		return errors.New("missing cookie or qrcode account")
	}

	return d.client.LoginCheck()
}

func (d *Pan115Share) transferAndFind(ctx context.Context, file FileObj, ua string) (*_115.Pan115, model.Obj, error) {

	virtualFile := virtual_file.GetSubscription(d.ID, file.GetPath())

	receiveResp := struct {
		State bool   `json:"state"`
		Error string `json:"error"`
		Errno int    `json:"errno"`
		Data  struct {
			PID          int64  `json:"pid"`
			ReceiveTitle string `json:"receive_title"`
			ReceiveSize  int64  `json:"receive_size"`
		} `json:"data"`
	}{}

	req := d.client.NewRequest().
		SetFormData(map[string]string{
			"share_code":   virtualFile.ShareID,
			"receive_code": virtualFile.SharePwd,
			"file_id":      file.GetID(),
			"cid":          d.CID,
		}).
		SetHeader("User-Agent", ua).
		SetResult(&receiveResp).
		ForceContentType("application/json;charset=UTF-8")

	resp, err := req.Post("https://115cdn.com/webapi/share/receive")
	if err != nil {
		return nil, nil, err
	}
	if resp == nil || !receiveResp.State || receiveResp.Errno != 0 {
		if receiveResp.Error == "" {
			receiveResp.Error = "receive api failed"
		}
		return nil, nil, fmt.Errorf("receive api error: %s", receiveResp.Error)
	}
	if receiveResp.Data.ReceiveTitle == "" {
		return nil, nil, errors.New("receive_title is empty")
	}

	storage := op.GetBalancedStorage(d.DriverPath)
	pan115, ok := storage.(*_115.Pan115)
	if !ok {
		return nil, nil, errors.New("invalid pan115 storage")
	}

	listObjs, err := pan115.List(ctx, &model.Object{ID: d.CID}, model.ListArgs{})
	if err != nil {
		return nil, nil, err
	}

	var target model.Obj
	for _, obj := range listObjs {
		if strings.HasPrefix(obj.GetName(), file.RawFileName) {
			target = obj
			break
		}
	}
	if target == nil {
		return nil, nil, fmt.Errorf("transferred file not found: %s", receiveResp.Data.ReceiveTitle)
	}

	return pan115, target, nil
}
