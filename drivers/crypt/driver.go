package crypt

import (
	"bytes"
	"context"
	"fmt"
	"io"
	stdpath "path"
	"regexp"
	"strings"
	"sync"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	rcCrypt "github.com/rclone/rclone/backend/crypt"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/obscure"
	log "github.com/sirupsen/logrus"
)

type Crypt struct {
	model.Storage
	Addition
	cipher *rcCrypt.Cipher
}

const obfuscatedPrefix = "___Obfuscated___"

func (d *Crypt) Config() driver.Config {
	return config
}

func (d *Crypt) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Crypt) Init(ctx context.Context) error {
	// obfuscate credentials if it's updated or just created
	err := d.updateObfusParm(&d.Password)
	if err != nil {
		return fmt.Errorf("failed to obfuscate password: %w", err)
	}
	err = d.updateObfusParm(&d.Salt)
	if err != nil {
		return fmt.Errorf("failed to obfuscate salt: %w", err)
	}

	isCryptExt := regexp.MustCompile(`^[.][A-Za-z0-9-_]{2,}$`).MatchString
	if !isCryptExt(d.EncryptedSuffix) {
		return fmt.Errorf("EncryptedSuffix is Illegal")
	}
	d.FileNameEncoding = utils.GetNoneEmpty(d.FileNameEncoding, "base64")
	d.EncryptedSuffix = utils.GetNoneEmpty(d.EncryptedSuffix, ".bin")
	d.RemotePath = utils.FixAndCleanPath(d.RemotePath)

	p, _ := strings.CutPrefix(d.Password, obfuscatedPrefix)
	p2, _ := strings.CutPrefix(d.Salt, obfuscatedPrefix)
	config := configmap.Simple{
		"password":                  p,
		"password2":                 p2,
		"filename_encryption":       d.FileNameEnc,
		"directory_name_encryption": d.DirNameEnc,
		"filename_encoding":         d.FileNameEncoding,
		"suffix":                    d.EncryptedSuffix,
		"pass_bad_blocks":           "",
	}
	c, err := rcCrypt.NewCipher(config)
	if err != nil {
		return fmt.Errorf("failed to create Cipher: %w", err)
	}
	d.cipher = c

	return nil
}

func (d *Crypt) updateObfusParm(str *string) error {
	temp := *str
	if !strings.HasPrefix(temp, obfuscatedPrefix) {
		temp, err := obscure.Obscure(temp)
		if err != nil {
			return err
		}
		temp = obfuscatedPrefix + temp
		*str = temp
	}
	return nil
}

func (d *Crypt) Drop(ctx context.Context) error {
	return nil
}

func (d *Crypt) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	remoteFullPath := dir.GetPath()
	objs, err := fs.List(ctx, remoteFullPath, &fs.ListArgs{NoLog: true, Refresh: args.Refresh})
	// the obj must implement the model.SetPath interface
	// return objs, err
	if err != nil {
		return nil, err
	}

	result := make([]model.Obj, 0, len(objs))
	for _, obj := range objs {
		rawName := model.UnwrapObj(obj).GetName()
		if obj.IsDir() {
			name, err := d.cipher.DecryptDirName(rawName)
			if err != nil {
				// filter illegal files
				continue
			}
			if !d.ShowHidden && strings.HasPrefix(name, ".") {
				continue
			}
			result = append(result, &model.Object{
				Path:     stdpath.Join(remoteFullPath, rawName),
				Name:     name,
				Size:     0,
				Modified: obj.ModTime(),
				IsFolder: obj.IsDir(),
				Ctime:    obj.CreateTime(),
				// discarding hash as it's encrypted
			})
			continue
		}

		size, err := d.cipher.DecryptedSize(obj.GetSize())
		if err != nil {
			// filter illegal files
			continue
		}
		name, err := d.cipher.DecryptFileName(rawName)
		if err != nil {
			// filter illegal files
			continue
		}
		if !d.ShowHidden && strings.HasPrefix(name, ".") {
			continue
		}
		objRes := &model.Object{
			Path:     stdpath.Join(remoteFullPath, rawName),
			Name:     name,
			Size:     size,
			Modified: obj.ModTime(),
			IsFolder: obj.IsDir(),
			Ctime:    obj.CreateTime(),
			// discarding hash as it's encrypted
		}
		if !d.Thumbnail || !strings.HasPrefix(args.ReqPath, "/") {
			result = append(result, objRes)
			continue
		}
		thumbPath := stdpath.Join(args.ReqPath, ".thumbnails", name+".webp")
		thumb := fmt.Sprintf("%s/d%s?sign=%s",
			common.GetApiUrl(ctx),
			utils.EncodePath(thumbPath, true),
			sign.Sign(thumbPath))
		result = append(result, &model.ObjThumb{
			Object: *objRes,
			Thumbnail: model.Thumbnail{
				Thumbnail: thumb,
			},
		})
	}

	return result, nil
}

func (d *Crypt) Get(ctx context.Context, path string) (model.Obj, error) {
	if utils.PathEqual(path, "/") {
		return &model.Object{
			Name:     "Root",
			IsFolder: true,
			Path:     d.RemotePath,
		}, nil
	}

	remoteObj, remoteFullPath, err := d.getEncryptedObject(ctx, path)
	if err != nil {
		return nil, err
	}
	size := remoteObj.GetSize()
	name := model.UnwrapObj(remoteObj).GetName()
	if !remoteObj.IsDir() {
		size, err = d.cipher.DecryptedSize(size)
		if err != nil {
			log.Warnf("DecryptedSize failed for %s ,will use original size, err:%s", path, err)
		}
		name, err = d.cipher.DecryptFileName(name)
		if err != nil {
			log.Warnf("DecryptFileName failed for %s ,will use original name, err:%s", path, err)
		}
	} else {
		name, err = d.cipher.DecryptDirName(name)
		if err != nil {
			log.Warnf("DecryptDirName failed for %s ,will use original name, err:%s", path, err)
		}
	}
	obj := &model.Object{
		Path:     remoteFullPath,
		Name:     name,
		Size:     size,
		Modified: remoteObj.ModTime(),
		IsFolder: remoteObj.IsDir(),
	}
	return obj, nil
}

// https://github.com/rclone/rclone/blob/v1.67.0/backend/crypt/cipher.go#L37
const fileHeaderSize = 32

func (d *Crypt) Link(ctx context.Context, file model.Obj, _ model.LinkArgs) (*model.Link, error) {
	remoteStorage, remoteActualPath, err := op.GetStorageAndActualPath(file.GetPath())
	if err != nil {
		return nil, err
	}
	remoteLink, remoteFile, err := op.Link(ctx, remoteStorage, remoteActualPath, model.LinkArgs{})
	if err != nil {
		return nil, err
	}

	remoteSize := remoteLink.ContentLength
	if remoteSize <= 0 {
		remoteSize = remoteFile.GetSize()
	}
	rrf, err := stream.GetRangeReaderFromLink(remoteSize, remoteLink)
	if err != nil {
		_ = remoteLink.Close()
		return nil, fmt.Errorf("the remote storage driver need to be enhanced to support encrytion")
	}

	mu := &sync.Mutex{}
	var fileHeader []byte
	rangeReaderFunc := func(ctx context.Context, offset, limit int64) (io.ReadCloser, error) {
		length := limit
		if offset == 0 && limit > 0 {
			mu.Lock()
			if limit <= fileHeaderSize {
				defer mu.Unlock()
				if fileHeader != nil {
					return io.NopCloser(bytes.NewReader(fileHeader[:limit])), nil
				}
				length = fileHeaderSize
			} else if fileHeader == nil {
				defer mu.Unlock()
			} else {
				mu.Unlock()
			}
		}

		remoteReader, err := rrf.RangeRead(ctx, http_range.Range{Start: offset, Length: length})
		if err != nil {
			return nil, err
		}

		if offset == 0 && limit > 0 {
			fileHeader = make([]byte, fileHeaderSize)
			n, err := io.ReadFull(remoteReader, fileHeader)
			if n != fileHeaderSize {
				fileHeader = nil
				return nil, fmt.Errorf("failed to read all data: (expect =%d, actual =%d) %w", fileHeaderSize, n, err)
			}
			if limit <= fileHeaderSize {
				remoteReader.Close()
				return io.NopCloser(bytes.NewReader(fileHeader[:limit])), nil
			} else {
				remoteReader = utils.ReadCloser{
					Reader: io.MultiReader(bytes.NewReader(fileHeader), remoteReader),
					Closer: remoteReader,
				}
			}
		}
		return remoteReader, nil
	}
	return &model.Link{
		RangeReader: stream.RangeReaderFunc(func(ctx context.Context, httpRange http_range.Range) (io.ReadCloser, error) {
			readSeeker, err := d.cipher.DecryptDataSeek(ctx, rangeReaderFunc, httpRange.Start, httpRange.Length)
			if err != nil {
				return nil, err
			}
			return readSeeker, nil
		}),
		SyncClosers:      utils.NewSyncClosers(remoteLink),
		RequireReference: remoteLink.RequireReference,
	}, nil
}

func (d *Crypt) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	remoteStorage, remoteActualPath, err := op.GetStorageAndActualPath(parentDir.GetPath())
	if err != nil {
		return err
	}
	encryptedName := d.cipher.EncryptDirName(dirName)
	return op.MakeDir(ctx, remoteStorage, stdpath.Join(remoteActualPath, encryptedName))
}

func (d *Crypt) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	_, err := fs.Move(ctx, srcObj.GetPath(), dstDir.GetPath())
	return err
}

func (d *Crypt) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	remoteStorage, remoteActualPath, err := op.GetStorageAndActualPath(srcObj.GetPath())
	if err != nil {
		return err
	}
	var newEncryptedName string
	if srcObj.IsDir() {
		newEncryptedName = d.cipher.EncryptDirName(newName)
	} else {
		newEncryptedName = d.cipher.EncryptFileName(newName)
	}
	return op.Rename(ctx, remoteStorage, remoteActualPath, newEncryptedName)
}

func (d *Crypt) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	_, err := fs.Copy(ctx, srcObj.GetPath(), dstDir.GetPath())
	return err
}

func (d *Crypt) Remove(ctx context.Context, obj model.Obj) error {
	remoteStorage, remoteActualPath, err := op.GetStorageAndActualPath(obj.GetPath())
	if err != nil {
		return err
	}
	return op.Remove(ctx, remoteStorage, remoteActualPath)
}

func (d *Crypt) Put(ctx context.Context, dstDir model.Obj, streamer model.FileStreamer, up driver.UpdateProgress) error {
	remoteStorage, remoteActualPath, err := op.GetStorageAndActualPath(dstDir.GetPath())
	if err != nil {
		return err
	}

	// Encrypt the data into wrappedIn
	wrappedIn, err := d.cipher.EncryptData(streamer)
	if err != nil {
		return fmt.Errorf("failed to EncryptData: %w", err)
	}

	// doesn't support seekableStream, since rapid-upload is not working for encrypted data
	streamOut := &stream.FileStream{
		Obj: &model.Object{
			ID:       streamer.GetID(),
			Path:     streamer.GetPath(),
			Name:     d.cipher.EncryptFileName(streamer.GetName()),
			Size:     d.cipher.EncryptedSize(streamer.GetSize()),
			Modified: streamer.ModTime(),
			IsFolder: streamer.IsDir(),
		},
		Reader:            wrappedIn,
		Mimetype:          "application/octet-stream",
		ForceStreamUpload: true,
		Exist:             streamer.GetExist(),
	}
	return op.Put(ctx, remoteStorage, remoteActualPath, streamOut, up)
}

func (d *Crypt) GetDetails(ctx context.Context) (*model.StorageDetails, error) {
	remoteStorage, _, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		return nil, errs.NotImplement
	}
	remoteDetails, err := op.GetStorageDetails(ctx, remoteStorage)
	if err != nil {
		return nil, err
	}
	return &model.StorageDetails{
		DiskUsage: remoteDetails.DiskUsage,
	}, nil
}

func (d *Crypt) BatchMove(ctx context.Context, srcDir model.Obj, srcObjs []model.Obj, dstDir model.Obj, args model.BatchArgs) error {

	batchMover, ok := d.remoteStorage.(driver.BatchMove)
	if !ok {
		return errs.NotImplement
	}

	srcEncryptedObj, dstEncryptedObj, encryptedObjs, err := d.convertEncryptedObj(ctx, srcDir, srcObjs, args)
	if err != nil {
		return err
	}

	err = batchMover.BatchMove(ctx, srcEncryptedObj, encryptedObjs, dstEncryptedObj, args)
	if err != nil {
		return err
	}

	if remoteActualPath, err := d.getActualPathForRemote(srcDir.GetPath(), true); err != nil {
		log.Warnf("Failed to get actual path for remote storage: %v", err)
	} else {
		op.Cache.DeleteDirectory(d.remoteStorage, remoteActualPath)
	}

	if remoteActualPath, err := d.getActualPathForRemote(dstDir.GetPath(), true); err != nil {
		log.Warnf("Failed to get actual path for remote storage: %v", err)
	} else {
		op.Cache.DeleteDirectory(d.remoteStorage, remoteActualPath)
	}

	return nil

}

func (d *Crypt) BatchCopy(ctx context.Context, srcDir model.Obj, srcObjs []model.Obj, dstDir model.Obj, args model.BatchArgs) error {

	batchCopier, ok := d.remoteStorage.(driver.BatchCopy)
	if !ok {
		return errs.NotImplement
	}

	srcEncryptedObj, dstEncryptedObj, encryptedObjs, err := d.convertEncryptedObj(ctx, srcDir, srcObjs, args)
	if err != nil {
		return err
	}

	err = batchCopier.BatchCopy(ctx, srcEncryptedObj, encryptedObjs, dstEncryptedObj, args)
	if err != nil {
		return err
	}

	if remoteActualPath, err := d.getActualPathForRemote(dstDir.GetPath(), true); err != nil {
		log.Warnf("Failed to get actual path for remote storage: %v", err)
	} else {
		op.Cache.DeleteDirectory(d.remoteStorage, remoteActualPath)
	}

	return nil

}

func (d *Crypt) BatchRemove(ctx context.Context, batchRemoveObj model.BatchRemoveObj, args model.BatchArgs) error {

	batchRemover, ok := d.remoteStorage.(driver.BatchRemove)
	if !ok {
		return errs.NotImplement
	}

	srcEncryptedObj, _, encryptedObjs, err := d.convertEncryptedObj(ctx, batchRemoveObj.Dir, batchRemoveObj.RemoveObjs, args)
	if err != nil {
		return err
	}

	removeObj := model.BatchRemoveObj{
		Dir: srcEncryptedObj,
	}

	nameSetMap := make(map[string]bool, len(encryptedObjs))
	for _, obj := range batchRemoveObj.RemoveObjs {
		nameSetMap[obj.GetName()] = true
	}

	for _, obj := range encryptedObjs {
		decryptedName := ""
		if obj.IsDir() {
			decryptedName, _ = d.cipher.DecryptDirName(obj.GetName())
			if decryptedName == "" || !nameSetMap[decryptedName] {
				continue
			}
			removeObj.RemoveObjs = append(removeObj.RemoveObjs, obj)
		} else {
			decryptedName, _ = d.cipher.DecryptFileName(obj.GetName())
			if decryptedName == "" || !nameSetMap[decryptedName] {
				continue
			}
			removeObj.RemoveObjs = append(removeObj.RemoveObjs, obj)
		}
	}

	err = batchRemover.BatchRemove(ctx, removeObj, args)
	if err != nil {
		return err
	}

	if remoteActualPath, err := d.getActualPathForRemote(batchRemoveObj.Dir.GetPath(), true); err != nil {
		log.Warnf("Failed to get actual path for remote storage: %v", err)
	} else {
		op.Cache.DeleteDirectory(d.remoteStorage, remoteActualPath)
	}

	return nil
}

func (d *Crypt) BatchRename(ctx context.Context, batchRenameObj model.BatchRenameObj, args model.BatchArgs) error {

	batchRenamer, ok := d.remoteStorage.(driver.BatchRename)
	if !ok {
		return errs.NotImplement
	}

	convert, err := utils.SliceConvert(batchRenameObj.RenameObjs, func(src model.RenameObj) (model.Obj, error) {
		return &src, nil
	})
	if err != nil {
		return err
	}

	srcEncryptedObj, _, encryptedObjs, err := d.convertEncryptedObj(ctx, batchRenameObj.Dir, convert, args)
	if err != nil {
		return err
	}

	renameObj := model.BatchRenameObj{
		Dir: srcEncryptedObj,
	}

	nameSetMap := make(map[string]model.RenameObj, len(encryptedObjs))
	for _, obj := range batchRenameObj.RenameObjs {
		nameSetMap[obj.GetName()] = obj
	}

	for _, obj := range encryptedObjs {
		decryptedName := ""
		encryptedName := ""
		if obj.IsDir() {
			decryptedName, _ = d.cipher.DecryptDirName(obj.GetName())
			if decryptedName == "" {
				continue
			}

			newObj, exist := nameSetMap[decryptedName]
			if !exist {
				continue
			}

			encryptedName = d.cipher.EncryptDirName(newObj.NewName)
			renameObj.RenameObjs = append(renameObj.RenameObjs, model.RenameObj{
				Obj:     obj,
				NewName: encryptedName,
			})
		} else {
			decryptedName, _ = d.cipher.DecryptFileName(obj.GetName())
			if decryptedName == "" {
				continue
			}
			newObj, exist := nameSetMap[decryptedName]
			if !exist {
				continue
			}

			encryptedName = d.cipher.EncryptFileName(newObj.NewName)
			renameObj.RenameObjs = append(renameObj.RenameObjs, model.RenameObj{
				Obj:     obj,
				NewName: encryptedName,
			})
		}
	}

	err = batchRenamer.BatchRename(ctx, renameObj, args)
	if err != nil {
		return err
	}

	if remoteActualPath, err := d.getActualPathForRemote(batchRenameObj.Dir.GetPath(), true); err != nil {
		log.Warnf("Failed to get actual path for remote storage: %v", err)
	} else {
		op.Cache.DeleteDirectory(d.remoteStorage, remoteActualPath)
	}

	return nil

}

var _ driver.Driver = (*Crypt)(nil)
