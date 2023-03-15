package generator

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

const (
	constIdFormat       = "%s-%s%s"
	constIncrementStep  = 1000
	LeastAvailableIdNum = 50 //当剩余可用id数小于这个数时，申请新号段，建议小于步长较多
)

type ApplyReq struct {
	AppName string `json:"appName"` //"申请应用名"
	BizType string `json:"bizType"` //应用内使用号段的业务类型，业务方需要确保appName + bizType 不与其它申请者重复
	Day     string `json:"day"`     //"日期格式: 20060102" 号段应用日期，获得的号段会确保该日期内独占（在appName+bizType范围内独点）
	Step    int    `json:"step"`    //"号段步长" 申请号段的步长, 建议申请步长为1000，或不超过100000
}

type NewRangeResp struct {
	RangeStart int64 `json:"rangeStart"`
	RangeEnd   int64 `json:"rangeEnd"`
}

type RangeUsageInfoStruct struct {
	//CurrentRangeStart int
	currentMaxId          int64
	currentRangeEnd       int64
	applyDate             time.Time
	usageM                sync.Mutex //控制共享变量更新
	gettingIdRangeCounter int32      //控制并发请求号段
	reqNumbersCaller      NumbersReqFunc
	logs                  LogInterface
}

type LogInterface interface {
	Debug(format string, v ...any)
	Info(format string, v ...any)
	Warn(format string, v ...any)
	Error(format string, v ...any)
}

type NumbersReqFunc func(req *ApplyReq) (*NewRangeResp, error)

var keyMap = map[byte]byte{
	'0': 'A',
	'1': 'C',
	'2': 'E',
	'3': 'F',
	'4': 'H',
	'5': 'N',
	'6': 'Q',
	'7': 'R',
	'8': 'S',
	'9': 'U',
}

func NewRangeUsage(caller NumbersReqFunc, logs LogInterface) *RangeUsageInfoStruct {
	return &RangeUsageInfoStruct{
		reqNumbersCaller: caller,
		logs:             logs,
	}
}

func (usage *RangeUsageInfoStruct) GenerateId(prefix, appName, bizType string) (string, error) {

	currentTime := time.Now()
	var currentId int64
	//根据当前号段资源，构建订单号
	todayFormat := currentTime.Format("20060102")
	req := ApplyReq{
		AppName: appName,
		BizType: bizType,
		Day:     todayFormat,
		Step:    constIncrementStep,
	}

	usage.logs.Debug("{} {} {} 请求新的id, 当前号段: {} {} {}", appName, bizType, prefix, usage.applyDate, usage.currentMaxId, usage.currentRangeEnd)

	if currentTime.Day() != usage.applyDate.Day() { //新的一天或服务重启了，获取新的号段
		usage.logs.Debug("{} {} {} 新的一天，取新号段", appName, bizType, prefix)
		resp, bSuccess := usage.getNewIdRange(&req)
		if !bSuccess {
			usage.logs.Debug("{} {} {} 当天初始号段已经在获取中，不再重复请求", appName, bizType, prefix)
			//return "", errcode.IdGenFailed.Error()
		} else {
			currentId = usage.replaceRange(resp.RangeStart, resp.RangeEnd, currentTime)
		}
	} else if usage.currentMaxId+LeastAvailableIdNum > usage.currentRangeEnd {
		//号段即将用完，获取新号段
		usage.logs.Debug("{} {} {} 当天号段用完了，重新申请", appName, bizType, prefix)
		resp, bSuccess := usage.getNewIdRange(&req)
		if !bSuccess {
			usage.logs.Debug("{} {} {} 当天初始号段已经在获取中，不再重复请求", appName, bizType, prefix)
			//return "", errcode.IdGenFailed.Error()
		} else {
			currentId = usage.replaceRange(resp.RangeStart, resp.RangeEnd, currentTime)
		}
	} else {
		//号段内递增
		currentId = usage.incrementAndGet()
		usage.logs.Debug("{} {} {} 使用已有号段获得的号码 {}", appName, bizType, prefix, currentId)
	}

	if (currentId == 0) || (currentId > usage.currentRangeEnd) {
		//号段获取失败
		//当前号段资源已用完且还未请求到新号段（高并发下低概率），降级到随机生成方案
		usage.logs.Warn("{} {} {} 获取号段失败或等待请求号段中，先降级到随机生成业务编号方案", appName, bizType, prefix)
		randSuffix := randId()
		randOrderId := fmt.Sprintf(constIdFormat, prefix, todayFormat, randSuffix)
		return randOrderId, nil
	}

	uniqueKey := fmt.Sprintf("%05d", currentId)

	uniqueKeyLen := len(uniqueKey)

	suffix := make([]byte, 0)
	for i := 0; i < uniqueKeyLen; i++ {
		ch := uniqueKey[i]
		newCh, ok := keyMap[ch]
		if !ok {
			usage.logs.Error("{} {} {} 生成id映射出错 {} {} {}", appName, bizType, prefix, uniqueKey, i, ch)
			return "", errors.New("id map error")
		}
		//newCh := uniqueKey[i] + 'A'
		suffix = append(suffix, newCh)
	}

	orderId := fmt.Sprintf(constIdFormat, prefix, todayFormat, suffix)

	//usage.logs.Debug("生成的业务编号 {}", orderId)
	return orderId, nil
}

func randId() string {
	rand.Seed(time.Now().UnixNano())
	suffix := make([]byte, 0)
	suffix = append(suffix, 'Y') //指定首个为'Y'字符，确保与自增号段空间隔离
	for i := 0; i < 8; i++ {
		pos := rand.Intn(26)
		pos += 'A'
		suffix = append(suffix, uint8(pos))
	}
	return string(suffix)
}

func (usage *RangeUsageInfoStruct) replaceRange(rangeStart, rangeEnd int64, usageDay time.Time) int64 {
	usage.usageM.Lock()
	defer usage.usageM.Unlock()
	if usage.applyDate == usageDay && usage.currentRangeEnd >= rangeEnd {
		usage.logs.Debug("不能用小的号段代替大的号段，直接递增")
		usage.currentMaxId++
		return usage.currentMaxId
	}
	usage.logs.Debug("号段更替，原号段 {} {} {} {}", usage.currentMaxId, usage.currentRangeEnd, usage.applyDate)
	usage.currentMaxId = rangeStart
	usage.currentRangeEnd = rangeEnd
	usage.applyDate = usageDay
	usage.logs.Debug("号段更替，新号段 {} {} {} {}", usage.currentMaxId, usage.currentRangeEnd, usage.applyDate)
	return usage.currentMaxId
}

func (usage *RangeUsageInfoStruct) incrementAndGet() int64 {
	usage.usageM.Lock()
	defer usage.usageM.Unlock()
	usage.currentMaxId++
	return usage.currentMaxId
}

func (usage *RangeUsageInfoStruct) getNewIdRange(req *ApplyReq) (*NewRangeResp, bool) {

	var curCounter int32
	curCounter = atomic.AddInt32(&(usage.gettingIdRangeCounter), 1)
	defer atomic.AddInt32(&(usage.gettingIdRangeCounter), -1)
	if curCounter > 1 { //已经有请求在进行了，不必再请求
		usage.logs.Debug("已有号段申请进行中 {}", curCounter)
		return nil, false
	}

	//logs.Debug("执行号段申请 {}", curCounter)
	resp, err := usage.reqNumbersCaller(req)
	if err != nil {
		usage.logs.Debug("号段申请失败 {} {}", err.Error(), curCounter)
		return nil, false
	}

	//logs.Debug("号段申请成功 {}", curCounter)
	return resp, true

}
