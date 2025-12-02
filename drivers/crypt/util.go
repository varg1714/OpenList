package crypt

import (
	"context"
	stdpath "path"
	"path/filepath"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	log "github.com/sirupsen/logrus"
)

// will give the best guessing based on the path
func guessPath(path string) (isFolder, secondTry bool) {
	if strings.HasSuffix(path, "/") {
		//confirmed a folder
		return true, false
	}
	lastSlash := strings.LastIndex(path, "/")
	if !strings.Contains(path[lastSlash:], ".") {
		//no dot, try folder then try file
		return true, true
	}
	return false, true
}

func (d *Crypt) encryptPath(path string, isFolder bool) string {
	if isFolder {
		return d.cipher.EncryptDirName(path)
	}
	dir, fileName := filepath.Split(path)
	return stdpath.Join(d.cipher.EncryptDirName(dir), d.cipher.EncryptFileName(fileName))
}

func (d *Crypt) getEncryptedObject(ctx context.Context, path string) (model.Obj, string, error) {

	firstTryIsFolder, secondTry := guessPath(path)
	remoteFullPath := stdpath.Join(d.RemotePath, d.encryptPath(path, firstTryIsFolder))
	remoteObj, err := fs.Get(ctx, remoteFullPath, &fs.GetArgs{NoLog: true})
	if err != nil {
		if secondTry && errs.IsObjectNotFound(err) {
			// try the opposite
			remoteFullPath = stdpath.Join(d.RemotePath, d.encryptPath(path, !firstTryIsFolder))
			remoteObj, err = fs.Get(ctx, remoteFullPath, &fs.GetArgs{NoLog: true})
			if err != nil {
				return nil, "", err
			}
		} else {
			return nil, "", err
		}
	}

	return remoteObj, remoteFullPath, nil
}

func (d *Crypt) convertEncryptedObj(ctx context.Context, srcDir model.Obj, srcObjs []model.Obj, args model.BatchArgs) (model.Obj, model.Obj, []model.Obj, error) {

	srcEncryptedObj,_, err := d.getEncryptedObject(ctx, args.SrcDirActualPath)
	if err != nil {
		return nil, nil, nil, err
	}

	var dstEncryptedObj model.Obj
	if args.DstDirActualPath != "" {
		dstEncryptedObj,_, err = d.getEncryptedObject(ctx, args.DstDirActualPath)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	objs, err := fs.List(ctx, srcDir.GetPath(), &fs.ListArgs{NoLog: true, Refresh: false})
	if err != nil {
		return nil, nil, nil, err
	}

	nameSet := make(map[string]bool)
	for _, obj := range srcObjs {
		nameSet[obj.GetName()] = true
	}

	var encryptedObjs []model.Obj
	for _, obj := range objs {
		if obj.IsDir() {
			dirName, err1 := d.cipher.DecryptDirName(obj.GetName())
			if err1 != nil {
				log.Warnf("failed to decrypt dir name: %v", err1)
				continue
			}
			if nameSet[dirName] {
				encryptedObjs = append(encryptedObjs, obj)
			}
		} else {
			fileName, err1 := d.cipher.DecryptFileName(obj.GetName())
			if err1 != nil {
				log.Warnf("failed to decrypt file name: %v", err1)
				continue
			}
			if nameSet[fileName] {
				encryptedObjs = append(encryptedObjs, obj)
			}
		}
	}
	return srcEncryptedObj, dstEncryptedObj, encryptedObjs, nil
}
