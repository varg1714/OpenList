package pornhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/go-resty/resty/v2"
	"github.com/robertkrimen/otto"
)

func (d *Pornhub) getVideoLink(ctx context.Context, viewKey string) (string, error) {
	client := resty.New().SetTimeout(30 * time.Second)
	res, err := client.R().SetContext(ctx).SetQueryParam("viewkey", viewKey).Get(fmt.Sprintf("%s/view_video.php", d.ServerUrl))
	if err != nil {
		utils.Log.Warnf("failed to get film page info from pornhub, %s", err.Error())
		return "", err
	}

	html := res.String()
	scriptRegexp := regexp.MustCompile(`<script\b[^>]*>([\s\S]*?)</script>`)
	matchers := scriptRegexp.FindAllStringSubmatch(html, -1)
	var encryptedScript string

	for _, scripts := range matchers {
		script := scripts[1]
		if !strings.Contains(script, "flashvars_") {
			continue
		}
		encryptedScript = script
		break
	}

	flashID := regexp.MustCompile(`flashvars_\d+`).FindString(encryptedScript)
	vm := otto.New()
	scriptCtx, cancelScript := context.WithTimeout(ctx, 30*time.Second)
	defer cancelScript()
	err = runPornhubScript(scriptCtx, vm, `var playerObjList = {};`+encryptedScript+fmt.Sprintf(`;var __VM__OUTPUT = JSON.stringify(%s.mediaDefinitions)`, flashID))
	if err != nil {
		utils.Log.Warnf("failed to run script, %s", err.Error())
		return "", err
	}

	value, err := vm.Get("__VM__OUTPUT")
	if err != nil {
		utils.Log.Warnf("failed to get console result, %v", err)
		return "", err
	}

	type mediaDefinition struct {
		Format   string `json:"format"`
		VideoURL string `json:"videoUrl"`
	}

	mediaDefinitions := make([]mediaDefinition, 0)
	str, err := value.ToString()
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal([]byte(str), &mediaDefinitions); err != nil {
		return "", err
	}

	var mp4MediaDefinition *mediaDefinition
	for _, definition := range mediaDefinitions {
		if definition.Format == "mp4" {
			mp4MediaDefinition = &definition
		}
	}
	if mp4MediaDefinition == nil {
		return "", errors.New("failed to find mp4 video")
	}

	pornVideos := make([]videoInfo, 0)
	_, err = client.R().SetContext(ctx).SetHeaders(map[string]string{
		"Referer": mp4MediaDefinition.VideoURL,
	}).SetResult(&pornVideos).Get(mp4MediaDefinition.VideoURL)
	if err != nil {
		return "", err
	}
	if len(pornVideos) == 0 {
		return "", errors.New("failed to find mp4 video")
	}

	return pornVideos[len(pornVideos)-1].VideoURL, nil
}

func runPornhubScript(ctx context.Context, vm *otto.Otto, script string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	interrupted := errors.New("pornhub script interrupted")
	vm.Interrupt = make(chan func(), 1)
	done := make(chan struct{})
	defer close(done)
	defer func() {
		if recovered := recover(); recovered != nil {
			if recovered == interrupted {
				err = ctx.Err()
				if err == nil {
					err = context.Canceled
				}
				return
			}
			panic(recovered)
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			select {
			case vm.Interrupt <- func() { panic(interrupted) }:
			case <-done:
			}
		case <-done:
		}
	}()

	_, err = vm.Run(script)
	return err
}
