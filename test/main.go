package main

import (
	"github.com/huangchunlong818/go-log-sls/log"
)

func main() {
	//配置
	config := log.LogConfig{
		Endpoint:        "xxx.aliyuncs.com",
		AccessKeyId:     "xxx",
		AccessKeySecret: "yyy",
		ProjectName:     "xxx",
		LogStoreName:    "ttt",
		DingAccessToken: "ttt",
		DingSecret:      "ccc",
		Debug:           false, //如果是true 会直接打印
	}
	//获取操作实例
	logs := log.NewLogger(config)

	//直接写入日志
	logs.Info("我是测试的info日志啊")

	//写入复杂日志
	var logArr []log.Msg
	logArr = append(logArr, log.Msg{
		Key: "title",
		Val: "我是标题",
	})
	logArr = append(logArr, log.Msg{
		Key: "content",
		Val: "我是正文",
	})
	data := log.Message{
		Cate:    "product",
		Message: logArr,
	}
	logs.Error(data)

	//关闭日志
	logs.Close()
}
