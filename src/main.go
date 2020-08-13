package main

import (
	// #include "../bind/def.h"
	"C"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/Mrs4s/MiraiGo/client"
	"github.com/Mrs4s/MiraiGo/message"
	log "github.com/sirupsen/logrus"
	asciiart "github.com/yinghau76/go-ascii-art"
)

var botRegistry map[int64]*CQBot = make(map[int64]*CQBot)

func Check(err error) {
	if err != nil {
		log.Fatalf("遇到错误: %v", err)
	}
}

//export GoFree
func GoFree(p unsafe.Pointer) {
	C.free(p)
}

//export _login
func _login(uinC C.longlong, pw *C.char) uintptr {
	console := bufio.NewReader(os.Stdin)
	client.SystemDeviceInfo.ReadJson([]byte("{\"display\":\"MIRAI.991110.001\",\"finger_print\":\"mamoe/mirai/mirai:10/MIRAI.200122.001/3854695:user/release-keys\",\"boot_id\":\"3B51B494-F2B9-6577-045F-D9CC60EAAB2C\",\"proc_version\":\"Linux version 3.0.31-BOECBqqM (android-build@xxx.xxx.xxx.xxx.com)\",\"imei\":\"116708152627273\"}"))
	uin := int64(uinC)
	cli := client.NewClient(uin, C.GoString(pw))
	// TODO error handling
	rsp, err := cli.Login()
	for {
		Check(err)
		if !rsp.Success {
			switch rsp.Error {
			case client.NeedCaptcha:
				_ = ioutil.WriteFile("captcha.jpg", rsp.CaptchaImage, os.ModePerm)
				img, _, _ := image.Decode(bytes.NewReader(rsp.CaptchaImage))
				fmt.Println(asciiart.New("image", img).Art)
				log.Warn("请输入验证码 (captcha.jpg)： (Enter 提交)")
				text, _ := console.ReadString('\n')
				rsp, err = cli.SubmitCaptcha(strings.ReplaceAll(text, "\n", ""), rsp.CaptchaSign)
				continue
			case client.UnsafeDeviceError:
				log.Warnf("账号已开启设备锁，请前往 -> %v <- 验证并重启Bot.", rsp.VerifyUrl)
				log.Infof(" 按 Enter 继续....")
				_, _ = console.ReadString('\n')
				return 0
			case client.OtherLoginError, client.UnknownLoginError:
				log.Fatalf("登录失败: %v", rsp.ErrorMessage)
			}
		}
		break
	}
	log.Info("开始加载好友列表...")
	Check(cli.ReloadFriendList())
	log.Infof("共加载 %v 个好友.", len(cli.FriendList))
	log.Infof("开始加载群列表...")
	Check(cli.ReloadGroupList())
	log.Infof("共加载 %v 个群.", len(cli.GroupList))
	ptr := &CQBot{
		Client: cli,
	}
	botRegistry[uin] = ptr
	log.Infof("登录成功: %v", cli.Nickname)
	return uintptr(unsafe.Pointer(ptr))
}

//export _onPrivateMessage
func _onPrivateMessage(botC unsafe.Pointer, cb C.ByteCallback, ctx C.uintptr_t) {
	bot := (*CQBot)(botC)
	bot.Client.OnPrivateMessage(func(c *client.QQClient, m *message.PrivateMessage) {
		cqm := ToStringMessage(m.Elements, 0, true)
		b, err := json.Marshal(MSG{
			"post_type":    "message",
			"message_type": "private",
			"sub_type":     "friend",
			"message_id":   ToGlobalId(m.Sender.Uin, m.Id),
			"user_id":      m.Sender.Uin,
			"message":      ToFormattedMessage(m.Elements, 0, false),
			"raw_message":  cqm,
			"font":         0,
			"self_id":      c.Uin,
			"time":         time.Now().Unix(),
			"sender": MSG{
				"user_id":  m.Sender.Uin,
				"nickname": m.Sender.Nickname,
				"sex":      "unknown",
				"age":      0,
			},
		})
		if err != nil {
			log.Infof("遇到错误: %v", err)
			return
		}
		log.Infof("收到好友 %v(%v) 的消息: %v", m.Sender.DisplayName(), m.Sender.Uin, cqm)
		C.InvokeByteCallback(cb, ctx, unsafe.Pointer(&b[0]), nil, C.size_t(len(b)))
	})
}

type CQBot struct {
	Client *client.QQClient

	events          []func(MSG)
	friendReqCache  sync.Map
	invitedReqCache sync.Map
	joinReqCache    sync.Map
	tempMsgCache    sync.Map
}

type MSG map[string]interface{}

func ToGlobalId(code int64, msgId int32) int32 {
	return int32(crc32.ChecksumIEEE([]byte(fmt.Sprintf("%d-%d", code, msgId))))
}

func ToFormattedMessage(e []message.IMessageElement, code int64, raw ...bool) (r interface{}) {
	r = ToStringMessage(e, code, raw...)
	return
}

func CQCodeEscapeText(raw string) string {
	ret := raw
	ret = strings.ReplaceAll(ret, "&", "&amp;")
	ret = strings.ReplaceAll(ret, "[", "&#91;")
	ret = strings.ReplaceAll(ret, "]", "&#93;")
	return ret
}

func CQCodeEscapeValue(value string) string {
	ret := CQCodeEscapeText(value)
	ret = strings.ReplaceAll(ret, ",", "&#44;")
	return ret
}

func ToStringMessage(e []message.IMessageElement, code int64, raw ...bool) (r string) {
	ur := false
	if len(raw) != 0 {
		ur = raw[0]
	}
	for _, elem := range e {
		switch o := elem.(type) {
		case *message.TextElement:
			r += CQCodeEscapeText(o.Content)
		case *message.AtElement:
			if o.Target == 0 {
				r += "[CQ:at,qq=all]"
				continue
			}
			r += fmt.Sprintf("[CQ:at,qq=%d]", o.Target)
		case *message.ReplyElement:
			r += fmt.Sprintf("[CQ:reply,id=%d]", ToGlobalId(code, o.ReplySeq))
		case *message.ForwardElement:
			r += fmt.Sprintf("[CQ:forward,id=%s]", o.ResId)
		case *message.FaceElement:
			r += fmt.Sprintf(`[CQ:face,id=%d]`, o.Index)
		case *message.VoiceElement:
			if ur {
				r += fmt.Sprintf(`[CQ:record,file=%s]`, o.Name)
			} else {
				r += fmt.Sprintf(`[CQ:record,file=%s,url=%s]`, o.Name, CQCodeEscapeValue(o.Url))
			}
		case *message.ImageElement:
			if ur {
				r += fmt.Sprintf(`[CQ:image,file=%s]`, o.Filename)
			} else {
				r += fmt.Sprintf(`[CQ:image,file=%s,url=%s]`, o.Filename, CQCodeEscapeValue(o.Url))
			}
		}
	}
	return
}

func main() {
}

//export getFriendList
func getFriendList(botC unsafe.Pointer) *C.char {
	bot := (*CQBot)(botC)
	var fs []MSG
	for _, f := range bot.Client.FriendList {
		fs = append(fs, MSG{
			"nickname": f.Nickname,
			"remark":   f.Remark,
			"user_id":  f.Uin,
		})
	}
	b, _ := json.Marshal(fs)
	return C.CString(string(b))
}

//export getGroupList
func getGroupList(botC unsafe.Pointer) *C.char {
	bot := (*CQBot)(botC)
	var gs []MSG
	for _, g := range bot.Client.GroupList {
		gs = append(gs, MSG{
			"group_id":         g.Code,
			"group_name":       g.Name,
			"max_member_count": g.MaxMemberCount,
			"member_count":     g.MemberCount,
		})
	}
	b, _ := json.Marshal(gs)
	return C.CString(string(b))
}

//export getGroupInfo
func getGroupInfo(botC unsafe.Pointer, groupId int64) *C.char {
	bot := (*CQBot)(botC)
	group := bot.Client.FindGroup(groupId)
	if group == nil {
		return C.CString("null")
	}
	b, _ := json.Marshal(MSG{
		"group_id":         group.Code,
		"group_name":       group.Name,
		"max_member_count": group.MaxMemberCount,
		"member_count":     group.MemberCount,
	})
	return C.CString(string(b))
}

//export getGroupMemberList
func getGroupMemberList(botC unsafe.Pointer, groupId int64) *C.char {
	bot := (*CQBot)(botC)
	group := bot.Client.FindGroup(groupId)
	if group == nil {
		return C.CString("null")
	}
	var members []MSG
	for _, m := range group.Members {
		members = append(members, convertGroupMemberInfo(groupId, m))
	}
	b, _ := json.Marshal(members)
	return C.CString(string(b))
}

func convertGroupMemberInfo(groupId int64, m *client.GroupMemberInfo) MSG {
	return MSG{
		"group_id":       groupId,
		"user_id":        m.Uin,
		"nickname":       m.Nickname,
		"card":           m.CardName,
		"sex":            "unknown",
		"age":            0,
		"area":           "",
		"join_time":      m.JoinTime,
		"last_sent_time": m.LastSpeakTime,
		"level":          strconv.FormatInt(int64(m.Level), 10),
		"role": func() string {
			switch m.Permission {
			case client.Owner:
				return "owner"
			case client.Administrator:
				return "admin"
			default:
				return "member"
			}
		}(),
		"unfriendly":        false,
		"title":             m.SpecialTitle,
		"title_expire_time": m.SpecialTitleExpireTime,
		"card_changeable":   false,
	}
}
