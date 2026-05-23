package open_ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

var TIMEOUT = 120 * time.Second

func Translate(text string) string {

	openAiUrl := setting.GetStr(conf.OpenAiUrl)
	openAiApiKey := setting.GetStr(conf.OpenAiApiKey)
	translatePromote := setting.GetStr(conf.OpenAiTranslatePromote)
	translateModel := setting.GetStr(conf.OpenAiTranslateModel)

	if openAiUrl == "" || openAiApiKey == "" {
		return text
	}

	execTranslateFunc := func(model, text string) string {

		var result struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}

		utils.Log.Debugf("开始翻译:%s", text)
		response, err := base.RestyClient.SetTimeout(TIMEOUT).R().SetAuthToken(openAiApiKey).SetHeaders(map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		}).SetBody(base.Json{
			"messages": func() []map[string]any {

				var param []map[string]any

				if translatePromote != "" {
					param = append(param, map[string]any{
						"role":    "system",
						"content": translatePromote,
					})
				}

				param = append(param, map[string]any{
					"role":    "user",
					"content": text,
				})

				return param
			}(),
			"model":             model,
			"temperature":       0.5,
			"presence_penalty":  0,
			"frequency_penalty": 0,
			"top_p":             1,
		}).SetResult(&result).Post(fmt.Sprintf("%s/v1/chat/completions", openAiUrl))

		if err != nil {
			var detail string
			if response != nil {
				detail = string(response.Body())
			}
			utils.Log.Warnf("翻译失败:%s,响应信息为:%s", err.Error(), detail)
			return ""
		}

		if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
			utils.Log.Warnf("翻译结果为空,响应信息为:%s", response.String())
			return ""
		}

		return result.Choices[0].Message.Content
	}

	for _, model := range strings.Split(translateModel, ",") {
		ans := execTranslateFunc(model, text)
		if ans != "" {
			return ans
		}
	}

	return text

}

type TranslateItem struct {
	Origin    string `json:"origin"`
	Candidate string `json:"candidate"`
}

func BatchTranslate(items []TranslateItem) []string {

	if len(items) == 0 {
		return nil
	}

	openAiUrl := setting.GetStr(conf.OpenAiUrl)
	openAiApiKey := setting.GetStr(conf.OpenAiApiKey)
	translateModel := setting.GetStr(conf.OpenAiTranslateModel)

	if openAiUrl == "" || openAiApiKey == "" {
		results := make([]string, len(items))
		for i, item := range items {
			if item.Candidate != "" {
				results[i] = item.Candidate
			} else {
				results[i] = item.Origin
			}
		}
		return results
	}

	jsonInput, _ := json.Marshal(items)
	basePrompt := fmt.Sprintf(`对于以下JSON数组中的每个对象：
- 如果candidate已经是通顺的中文，保留candidate
- 如果candidate不是中文或不流畅，根据origin重新翻译为中文
- 如果candidate为空，根据origin翻译为中文
只返回相同顺序的JSON字符串数组，不要其他内容。
输出示例：["翻译1","翻译2","翻译3"]
输入数据：
%s`, string(jsonInput))

	tryTranslate := func(model, prompt string) ([]string, string) {

		translatePromote := setting.GetStr(conf.OpenAiTranslatePromote)

		var param []map[string]any
		if translatePromote != "" {
			param = append(param, map[string]any{
				"role":    "system",
				"content": translatePromote,
			})
		}
		param = append(param, map[string]any{
			"role":    "user",
			"content": prompt,
		})

		var result struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}

		utils.Log.Debugf("开始批量翻译:%d个文本", len(items))
		response, err := base.RestyClient.SetTimeout(TIMEOUT).R().SetAuthToken(openAiApiKey).SetHeaders(map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		}).SetBody(base.Json{
			"messages":          param,
			"model":             model,
			"temperature":       0.5,
			"presence_penalty":  0,
			"frequency_penalty": 0,
			"top_p":             1,
		}).SetResult(&result).Post(fmt.Sprintf("%s/v1/chat/completions", openAiUrl))

		if err != nil {
			var detail string
			if response != nil {
				detail = string(response.Body())
			}
			reason := fmt.Sprintf("API请求失败:%s,响应:%s", err.Error(), detail)
			utils.Log.Warnf("批量翻译失败:%s", reason)
			return nil, reason
		}

		if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
			return nil, "翻译结果为空"
		}

		content := strings.TrimSpace(result.Choices[0].Message.Content)
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)

		var translations []string
		if err := json.Unmarshal([]byte(content), &translations); err != nil {
			var altItems []TranslateItem
			if err2 := json.Unmarshal([]byte(content), &altItems); err2 != nil {
				reason := fmt.Sprintf("JSON解析失败(字符串数组:%s,对象数组:%s),原始响应:%s", err.Error(), err2.Error(), content)
				utils.Log.Warnf("批量翻译%s", reason)
				return nil, reason
			}
			for _, item := range altItems {
				if item.Candidate != "" {
					translations = append(translations, item.Candidate)
				} else {
					translations = append(translations, item.Origin)
				}
			}
		}

		if len(translations) != len(items) {
			reason := fmt.Sprintf("翻译数量不匹配:期望%d,实际%d,响应:%s", len(items), len(translations), content)
			utils.Log.Warnf("批量翻译%s", reason)
			return nil, reason
		}

		return translations, ""
	}

	for _, model := range strings.Split(translateModel, ",") {
		prompt := basePrompt
		for attempt := 0; attempt < 3; attempt++ {
			translations, retryReason := tryTranslate(model, prompt)
			if translations != nil {
				return translations
			}
			prompt = basePrompt + fmt.Sprintf("\n\n上次翻译出错：%s\n请重试，确保返回恰好%d个翻译的JSON数组。", retryReason, len(items))
			utils.Log.Warnf("批量翻译第%d次重试", attempt+1)
		}
	}

	return make([]string, len(items))

}
