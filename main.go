package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/lionsoul2014/ip2region/binding/golang/ip2region"
	"github.com/thinkeridea/go-extend/exnet"
)

var (
	wg    = sync.WaitGroup{}
	port  = ""
	d     = "" // 下载标识
	dbUrl = map[string]string{
		"1": "https://github.do/https://raw.githubusercontent.com/lionsoul2014/ip2region/raw/master/data/ip2region.db",
		"2": "https://github.do/https://raw.githubusercontent.com/bqf9979/ip2region/raw/master/data/ip2region.db",
	}
)

const (
	ipDbPath     = "./ip2region.db"
	defaultDbUrl = "2" // 默认下载 来自 bqf9979 仓库的 ip db文件,在维护，且有付费资源
)

type JsonRes struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data"`
}

type IpInfo struct {
	Ip       string `json:"ip"`
	Country  string `json:"country"`  // 国家
	Province string `json:"province"` // 省
	City     string `json:"city"`     // 市
	County   string `json:"county"`   // 县、区
	Region   string `json:"region"`   // 区域位置
	ISP      string `json:"isp"`      // 互联网服务提供商
}

func init() {
	_p := flag.String("p", "9090", "本地监听的端口")
	_d := flag.String("d", "0", "仅用于下载最新的ip地址库，保存在当前目录")
	flag.Parse()

	port = *_p
	d = *_d

	if d != "0" {
		if value, ok := dbUrl[d]; ok {
			downloadIpDb(value)
		} else {
			downloadIpDb(dbUrl[defaultDbUrl])
		}
		os.Exit(1)
	}

	checkIpDbIsExist()
}

func main() {
	http.HandleFunc("/", queryIp)

	link := "http://0.0.0.0:" + port

	log.Println("监听端口", link)
	listenErr := http.ListenAndServe(":"+port, nil)
	if listenErr != nil {
		log.Fatal("ListenAndServe: ", listenErr)
	}
}

func QuerIP_go(ip string, response chan IpInfo, limiter chan bool, wg *sync.WaitGroup, region *ip2region.Ip2Region) {
	// fmt.Println()
	// fmt.Println("开始携程:\t", ip, time.Now(), "休眠3秒")
	// time.Sleep(3 * time.Second)
	// 函数执行完毕时 计数器-1
	defer wg.Done()
	// 将拿到的结果, 发送到参数中传递过来的channel中

	info, _ := region.MemorySearch(ip)
	rinfo := &IpInfo{
		Ip:       ip,
		ISP:      info.ISP,
		Country:  info.Country,
		Province: info.Province,
		City:     info.City,
		County:   "",
		Region:   info.Region,
	}
	response <- *rinfo
	// 释放一个坑位
	<-limiter
}

// 将所有的返回结果, 以 []string 的形式返回
func QuerIP(ips []string) []IpInfo {
	var result []IpInfo

	wg := &sync.WaitGroup{}
	// 控制并发数为10
	limiter := make(chan bool, 34)
	defer close(limiter)

	// 函数内的局部变量channel, 专门用来接收函数内所有goroutine的结果
	responseChannel := make(chan IpInfo, 34)
	// 为读取结果控制器创建新的WaitGroup, 需要保证控制器内的所有值都已经正确处理完毕, 才能结束
	wgResponse := &sync.WaitGroup{}
	// 启动读取结果的控制器
	// wgResponse计数器+1
	wgResponse.Add(1)
	go func() {
		// 读取结果
		for response := range responseChannel {
			// 处理结果
			result = append(result, response)
		}
		// 当 responseChannel被关闭时且channel中所有的值都已经被处理完毕后, 将执行到这一行
		wgResponse.Done()
	}()
	region, _ := ip2region.New("./ip2region.db")
	defer region.Close()
	for _, ip := range ips {
		// 计数器+1
		wg.Add(1)
		limiter <- true
		// 这里在启动goroutine时, 将用来收集结果的局部变量channel也传递进去
		go QuerIP_go(ip, responseChannel, limiter, wg, region)
	}

	// 等待所以协程执行完毕
	wg.Wait() // 当计数器为0时, 不再阻塞
	fmt.Println("所有协程已执行完毕")

	// 关闭接收结果channel
	close(responseChannel)

	// 等待wgResponse的计数器归零
	wgResponse.Wait()

	// 返回聚合后结果
	return result
}

func queryIp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "text/json;charset=utf-8")

	defer func() {
		//捕获 panic
		if err := recover(); err != nil {
			log.Println("查询ip发生错误", err)
		}
	}()

	if r.URL.Path != "/" {
		w.WriteHeader(404)
		msg, _ := json.Marshal(&JsonRes{Code: 4000, Msg: r.URL.Path + " 404 NOT FOUND !"})
		w.Write(msg)
		return
	}

	r.ParseForm() // 解析参数

	ip := r.FormValue("ip")

	if ip == "" {
		// 获取当前客户端 IP
		ip = getIp(r)
	}

	region, err := ip2region.New(ipDbPath)
	defer region.Close()

	if err != nil {
		msg, _ := json.Marshal(&JsonRes{Code: 4001, Msg: err.Error()})
		w.Write(msg)
		return
	}

	// 分割字符串

	ip_arr := strings.Split(ip, ",")
	result := QuerIP(ip_arr)
	msg, _ := json.Marshal(JsonRes{Code: 200, Data: result})
	w.Write(msg)
	return
}

func getIp(r *http.Request) string {
	ip := exnet.ClientPublicIP(r)
	if ip == "" {
		ip = exnet.ClientIP(r)
	}
	return ip
}

func checkIpDbIsExist() {
	if _, err := os.Stat(ipDbPath); os.IsNotExist(err) {
		log.Println("ip 地址库文件不存在")
		downloadIpDb(dbUrl[defaultDbUrl])
	}
}

func downloadIpDb(url string) {
	log.Println("正在下载最新的 ip 地址库...：" + url)
	wg.Add(1)
	go func() {
		downloadFile(ipDbPath, url)
		wg.Done()
	}()
	wg.Wait()
	log.Println("下载完成")
}

// @link https://studygolang.com/articles/26441
func downloadFile(filepath string, url string) error {
	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}
