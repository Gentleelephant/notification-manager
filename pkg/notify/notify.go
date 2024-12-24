package notify

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/duke-git/lancet/v2/convertor"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/kubesphere/notification-manager/pkg/async"
	"github.com/kubesphere/notification-manager/pkg/constants"
	"github.com/kubesphere/notification-manager/pkg/controller"
	"github.com/kubesphere/notification-manager/pkg/internal"
	"github.com/kubesphere/notification-manager/pkg/notify/notifier"
	"github.com/kubesphere/notification-manager/pkg/notify/notifier/dingtalk"
	"github.com/kubesphere/notification-manager/pkg/notify/notifier/discord"
	"github.com/kubesphere/notification-manager/pkg/notify/notifier/email"
	"github.com/kubesphere/notification-manager/pkg/notify/notifier/feishu"
	"github.com/kubesphere/notification-manager/pkg/notify/notifier/pushover"
	"github.com/kubesphere/notification-manager/pkg/notify/notifier/slack"
	"github.com/kubesphere/notification-manager/pkg/notify/notifier/sms"
	"github.com/kubesphere/notification-manager/pkg/notify/notifier/telegram"
	"github.com/kubesphere/notification-manager/pkg/notify/notifier/webhook"
	"github.com/kubesphere/notification-manager/pkg/notify/notifier/wechat"
	"github.com/kubesphere/notification-manager/pkg/stage"
	"github.com/kubesphere/notification-manager/pkg/template"
	"github.com/modern-go/reflect2"
)

type Factory func(logger log.Logger, receiver internal.Receiver, notifierCtl *controller.Controller) (notifier.Notifier, error)

var (
	factories map[string]Factory
)

func init() {
	Register(constants.Email, email.NewEmailNotifier)
	Register(constants.WeChat, wechat.NewWechatNotifier)
	Register(constants.Slack, slack.NewSlackNotifier)
	Register(constants.Webhook, webhook.NewWebhookNotifier)
	Register(constants.DingTalk, dingtalk.NewDingTalkNotifier)
	Register(constants.SMS, sms.NewSmsNotifier)
	Register(constants.Pushover, pushover.NewPushoverNotifier)
	Register(constants.Feishu, feishu.NewFeishuNotifier)
	Register(constants.Discord, discord.NewDiscordNotifier)
	Register(constants.Telegram, telegram.NewTelegramNotifier)
}

func Register(name string, factory Factory) {
	if factories == nil {
		factories = make(map[string]Factory)
	}

	factories[name] = factory
}

type notifyStage struct {
	notifierCtl *controller.Controller
}

func NewStage(notifierCtl *controller.Controller) stage.Stage {

	return &notifyStage{
		notifierCtl: notifierCtl,
	}
}

func (s *notifyStage) Exec(ctx context.Context, l log.Logger, data interface{}) (context.Context, interface{}, error) {

	if reflect2.IsNil(data) {
		return ctx, nil, nil
	}

	_ = level.Debug(l).Log("msg", "Start notify stage", "seq", ctx.Value("seq"))

	input := data.(map[internal.Receiver][]*template.Data)
	alertMap := make(map[string]*template.Alert)
	for _, dataList := range input {
		//receiver := r
		ds := convertor.DeepClone(dataList)
		//s.addExtensionLabels(receiver, ds)
		for _, d := range ds {
			for _, alert := range d.Alerts {
				alertMap[alert.ID] = alert
			}
		}
	}

	var mutex sync.Mutex
	handler := func(alerts []*template.Alert) {
		mutex.Lock()
		defer mutex.Unlock()

		for _, alert := range alerts {
			if a := alertMap[alert.ID]; a != nil {
				a.NotifySuccessful = true
			}
		}
	}

	var emailReceivers []string

	group := async.NewGroup(ctx)
	for k, v := range input {
		receiver := k
		ds := convertor.DeepClone(v)
		s.addExtensionLabels(receiver, ds)
		nf, err := factories[receiver.GetType()](l, receiver, s.notifierCtl)
		if err != nil {
			e := err
			group.Add(func(stopCh chan interface{}) {
				stopCh <- e
			})
			continue
		}
		if receiver.GetType() == constants.Email {
			emailReceivers = append(emailReceivers, receiver.GetName())
		}
		nf.SetSentSuccessfulHandler(&handler)

		for _, d := range ds {
			alert := d
			group.Add(func(stopCh chan interface{}) {
				stopCh <- nf.Notify(ctx, alert)
			})
		}
	}

	fmt.Printf("emailReceivers: %+v\n", emailReceivers)

	for _, alert := range alertMap {
		if alert.NotifySuccessful {
			alert.Labels[constants.EmailReceiverList] = strings.Join(emailReceivers, ",")
		}
	}

	//fmt.Printf("hisData: %+v\n", hisData)
	//
	//// 处理数据要返回给通知历史的数据
	//// 2. 将receivers保存到数据中
	//for _, d := range hisData {
	//	emailReceivers := d.CommonLabels[constants.EmailReceiverList]
	//	level.Debug(l).Log("msg", "====>common label emailReceivers<====:", emailReceivers)
	//	for _, alert := range d.Alerts {
	//		//delete(alertMap[alert.ID].Labels, constants.ReceiverName)
	//		alertMap[alert.ID].Labels[constants.EmailReceiverList] = emailReceivers
	//		level.Debug(l).Log("msg", "====>emailReceivers<====:", emailReceivers)
	//	}
	//}

	return ctx, alertMap, group.Wait()
}

func (s *notifyStage) addExtensionLabels(receiver internal.Receiver, data []*template.Data) {
	for _, d := range data {
		for _, alert := range d.Alerts {
			if alert.Labels[constants.ReceiverName] == "" {
				alert.Labels[constants.ReceiverName] = receiver.GetName()
			}
		}
	}
}
