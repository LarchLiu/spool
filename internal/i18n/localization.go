package i18n

import (
	"encoding/json"
	"strings"

	resources "spool"
)

var messages = loadMessages()

func loadMessages() map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, locale := range []string{"en", "zh-CN", "ja"} {
		body, err := resources.Locales().ReadFile("locales/" + locale + ".json")
		if err != nil {
			panic(err)
		}
		var catalog map[string]string
		if err = json.Unmarshal(body, &catalog); err != nil {
			panic(err)
		}
		out[locale] = catalog
	}
	return out
}

func ResolveLanguage(preference string, preferred ...string) string {
	if preference != "" && preference != "system" {
		preferred = []string{preference}
	}
	for _, tag := range preferred {
		tag = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"))
		switch strings.Split(tag, "-")[0] {
		case "zh":
			return "zh-CN"
		case "en":
			return "en"
		case "ja":
			return "ja"
		}
	}
	return "en"
}

// Message keys are English source strings, shared with the Web's typed catalog.
// Agent output, project names and other user-authored content stay untouched.
func Translate(locale, message string, values ...string) string {
	if key, ok := errorMessageKeys[message]; ok {
		message = key
	}
	if text, ok := messages[ResolveLanguage(locale)][message]; ok {
		message = text
	}
	for i := 0; i+1 < len(values); i += 2 {
		message = strings.ReplaceAll(message, "{"+values[i]+"}", values[i+1])
	}
	return message
}

var errorMessageKeys = map[string]string{
	"跨域登录被拒绝":      "Cross-origin login rejected",
	"登录尝试过多，请稍后重试": "Too many login attempts. Try again later.",
	"用户名或密码不正确":    "Incorrect username or password",
	"请先登录":         "Please sign in",
	"退出失败":         "Could not sign out",
	"读取项目失败":       "Could not load projects",
	"读取 agents 失败": "Could not load agents",
	"模型探测失败，已保留上次成功的列表；请联系管理员。": "Model discovery failed. The last successful list is retained. Contact your administrator.",
	"无权访问该项目":         "You do not have access to this project",
	"agent 未启用或尚未安装":  "The agent is disabled or not installed",
	"无权在该项目创建聊天":      "You cannot create a chat in this project",
	"接口不存在":           "API endpoint not found",
	"聊天不存在":           "Chat not found",
	"请求来源或 CSRF 校验失败": "Request origin or CSRF check failed",
	"无效的 JSON 请求":     "Invalid JSON request",
	"只允许一个 JSON 对象":   "Only one JSON object is allowed",
}
