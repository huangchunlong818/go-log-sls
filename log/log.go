package log

import (
	"encoding/json"
	"fmt"
	sls "github.com/aliyun/aliyun-log-go-sdk"
	"github.com/gogo/protobuf/proto"
	"github.com/wanghuiyt/ding"
	"strconv"
	"sync"
	"time"
)

type Logger struct {
	client    sls.ClientInterface
	project   string
	logstore  string
	logChan   chan *sls.LogGroup
	wg        sync.WaitGroup
	closed    bool
	closeChan chan struct{}
	ding      *ding.Webhook
	config    *LogConfig
}

// 配置
type LogConfig struct {
	Endpoint, AccessKeyId, AccessKeySecret, ProjectName, LogStoreName, DingAccessToken, DingSecret string
	LogChanNum                                                                                     int  //日志通道数量，默认1000
	LogConsumeNum                                                                                  int  //消费日志通道协程数量，默认3
	Debug                                                                                          bool //调试模式，如果是true，直接打印日志
}

// NewLogger 初始化日志系统
func NewLogger(config LogConfig) *Logger {
	var client sls.ClientInterface

	if !config.Debug {
		// RAM用户角色的临时安全令牌。此处取值为空，表示不使用临时安全令牌。
		SecurityToken := ""
		// 创建日志服务Client。
		provider := sls.NewStaticCredentialsProvider(config.AccessKeyId, config.AccessKeySecret, SecurityToken)
		client = sls.CreateNormalInterfaceV2(config.Endpoint, provider)

		//配置默认值
		if config.LogChanNum <= 0 {
			config.LogChanNum = 100 //日志通道数量，默认1000
		}
	}

	logSystem := &Logger{
		client:    client,
		project:   config.ProjectName,
		logstore:  config.LogStoreName,
		logChan:   make(chan *sls.LogGroup, config.LogChanNum), // 设置缓冲通道
		closeChan: make(chan struct{}),
		closed:    false,
		ding: &ding.Webhook{
			AccessToken: config.DingAccessToken,
			Secret:      config.DingSecret,
		},
		config: &config,
	}

	if !config.Debug {
		logSystem.dispatchLogs() // 异步推送日志
	}

	return logSystem
}

type Msg struct {
	Key string //日志内容key 必须
	Val string //日志文本内容 必须
}

type Message struct {
	Timezone string //时区，默认中国北京时间，好查看日志 可选
	Cate     string //归类，默认是空 可选
	Message  []Msg  //消息体
}

// Info 记录信息日志
func (l *Logger) Info(message any) {
	l.doLog(message, "Info")
}

// Error 记录信息日志
func (l *Logger) Error(message any) {
	l.doLog(message, "Error")
}

// Debug 记录信息日志
func (l *Logger) Debug(message any) {
	l.doLog(message, "Debug")
}

// Warn 记录信息日志
func (l *Logger) Warn(message any) {
	l.doLog(message, "Warn")
}

// DPanic 记录信息日志
func (l *Logger) DPanic(message any) {
	l.doLog(message, "DPanic")
}

// Panic  记录信息日志
func (l *Logger) Panic(message any) {
	l.doLog(message, "Panic")
}

// Fatal  记录信息日志
func (l *Logger) Fatal(message any) {
	l.doLog(message, "Fatal")
}

// 判断日志通道以及是否推送钉钉
func (l *Logger) doLog(message any, types string) {
	if l.config.Debug {
		fmt.Println("["+types+"]:", message)
		return
	}

	org, _ := json.Marshal(message)
	//判断是否关闭日志通道
	if l.Check() {
		_ = l.ding.SendMessageText(types + " 日志写入失败，日志通道已关闭，原始数据：" + string(org))
		return
	}

	var (
		cate    string
		content []*sls.LogContent
		msgs    []Msg
		timeS   uint32
	)
	//判断类型，只允许 string，Message 2种类型
	switch message.(type) {
	case string:
		// 加载上海时区
		timeS = l.getTimeUnix("")
		msgs = append(msgs, Msg{
			Key: "message",
			Val: message.(string),
		})
	case Message:
		// 加载指定时区
		tmp := message.(Message)
		timeS = l.getTimeUnix(tmp.Timezone)
		cate = tmp.Cate
		for _, v := range tmp.Message {
			msgs = append(msgs, Msg{
				Key: v.Key,
				Val: v.Val,
			})
		}
	default:
		_ = l.ding.SendMessageText(types + " 日志写入失败，只允许传递string, Message 格式类型数据，原始数据：" + string(org))
		return
	}

	//组装日志内容
	for _, msgTmp := range msgs {
		content = append(content, &sls.LogContent{
			Key:   proto.String(msgTmp.Key),
			Value: proto.String(msgTmp.Val),
		})
	}

	logs := []*sls.Log{}
	// 创建日志
	logTmp := &sls.Log{
		Time:     proto.Uint32(timeS), // 当前时间戳
		Contents: content,
	}
	logs = append(logs, logTmp)

	logGroup := &sls.LogGroup{
		Topic:    proto.String(types),
		Category: proto.String(cate),
		Logs:     logs,
	}

	l.logChan <- logGroup // 发送日志到通道
	l.wg.Add(1)           // 增加计数器
	return
}

// 根据指定时区获取时间戳
func (l *Logger) getTimeUnix(timeLocation string) uint32 {
	var timeS uint32
	if timeLocation != "" {
		//加载指定时区
		if location, err := time.LoadLocation(timeLocation); err == nil {
			timeS = uint32(time.Now().In(location).Unix())
		}
	}
	if timeS < 1 {
		// 加载默认时区
		if location, err := time.LoadLocation("Asia/Shanghai"); err != nil {
			timeS = uint32(time.Now().UTC().Unix())
		} else {
			timeS = uint32(time.Now().In(location).Unix())
		}
	}

	return timeS
}

// dispatchLogs 负责处理和推送日志
func (l *Logger) dispatchLogs() {
	if l.config.LogConsumeNum <= 0 {
		l.config.LogConsumeNum = 3 //默认3
	}
	for i := 0; i < l.config.LogConsumeNum; i++ {
		i := i
		go func() {
			fmt.Println("启动第" + strconv.Itoa(i) + "个协程")
			for {
				select {
				case logGroup := <-l.logChan:
					//正常推送日志到SLS
					if err := l.client.PutLogs(l.project, l.logstore, logGroup); err != nil {
						_ = l.ding.SendMessageText(" 日志推送SLS失败，err：" + err.Error())
					}
					l.wg.Done() // 完成日志处理
				case <-l.closeChan:
					//关闭日志通道
					l.closed = true
					return
				}
			}
		}()
	}
}

// Close 关闭日志系统
func (l *Logger) Close() {
	if !l.Check() {
		close(l.closeChan) // 关闭通道
		l.wg.Wait()        // 等待所有日志处理完成
	}
}

// 检查日志通道是否关闭
func (l *Logger) Check() bool {
	if l.closed {
		return true //已关闭
	}
	return false
}
