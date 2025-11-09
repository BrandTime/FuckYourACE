package main

import (
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// CloudConfig 定义了从云端下发的配置
type CloudConfig struct {
	ProcessList     []string `json:"process_list"`
	Announcement    string   `json:"announcement"`
	TotalExecutions uint64   `json:"total_executions"`
	OnlineUsers     int      `json:"online_users"`
}

// HeartbeatRequest 是客户端发送心跳的结构
type HeartbeatRequest struct {
	ClientID string `json:"client_id"`
}

// --- 全局变量 ---

// currentConfig 存储当前的云端配置
// !! 生产环境中，应从数据库或配置文件中读取
var currentConfig = CloudConfig{
	ProcessList: []string{
		"SGuard64.exe",
		"SGuardSvc64.exe",
		"winTargetProc3.exe",
		"winTargetProc4.exe",
	},
	Announcement: "🔥 云端公告：已支持新版防护！如有问题请联系管理员。",
}

// activeClients 用于存储活跃客户端的心跳
var activeClients = make(map[string]time.Time)
var clientMutex sync.RWMutex

// totalExecutions 存储总执行次数
var totalExecutions uint64

// --- 后台主程序 ---

func main() {
	// 启动一个goroutine来定期清理过期（掉线）的客户端
	go cleanupExpiredClients()

	router := gin.Default()
	gin.SetMode(gin.ReleaseMode) // 生产模式

	// 使用默认的 CORS 中间件，允许所有跨域请求
	router.Use(cors.Default())

	// 设置 API 路由
	api := router.Group("/api")
	{
		api.GET("/config", getConfigHandler)
		api.POST("/heartbeat", heartbeatHandler)
		api.GET("/stats", statsHandler) // 管理员统计接口
	}

	// 启动服务器
	log.Println("后台服务器启动于 0.0.0.0:8080")
	if err := router.Run(":8080"); err != nil {
		log.Fatalf("无法启动服务器: %v", err)
	}
}

// --- Gin 处理器 ---

// getConfigHandler 向客户端发送当前配置和统计
func getConfigHandler(c *gin.Context) {
	clientMutex.RLock()
	onlineCount := getActiveClientCount(5 * time.Minute) // 5分钟内活跃
	clientMutex.RUnlock()

	totalRuns := atomic.LoadUint64(&totalExecutions)

	// 复制静态配置并填充动态统计
	config := currentConfig
	config.OnlineUsers = onlineCount
	config.TotalExecutions = totalRuns

	c.JSON(http.StatusOK, config)
}

// heartbeatHandler 接收心跳，更新活跃时间，并增加总执行次数
func heartbeatHandler(c *gin.Context) {
	var req HeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求"})
		return
	}

	if req.ClientID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要 client_id"})
		return
	}

	// 记录客户端活跃时间
	clientMutex.Lock()
	activeClients[req.ClientID] = time.Now()
	clientMutex.Unlock()

	// 原子增加总执行次数
	atomic.AddUint64(&totalExecutions, 1)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// statsHandler (给管理员) 返回当前的活跃用户数和总执行次数
func statsHandler(c *gin.Context) {
	clientMutex.RLock()
	count := getActiveClientCount(5 * time.Minute)
	clientMutex.RUnlock()

	totalRuns := atomic.LoadUint64(&totalExecutions)

	c.JSON(http.StatusOK, gin.H{
		"active_users_5min": count,
		"total_executions":  totalRuns,
		"total_tracked":     len(activeClients),
	})
}

// --- 辅助函数 ---

// getActiveClientCount 计算在指定时间范围内有多少活跃客户端
func getActiveClientCount(duration time.Duration) int {
	count := 0
	cutoff := time.Now().Add(-duration)
	for _, lastSeen := range activeClients {
		if lastSeen.After(cutoff) {
			count++
		}
	}
	return count
}

// cleanupExpiredClients 定期清理那些长时间未发送心跳的客户端
func cleanupExpiredClients() {
	// 每10分钟清理一次
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		clientMutex.Lock()
		// 我们只保留最近1小时内活跃的客户端
		cutoff := time.Now().Add(-1 * time.Hour)
		for id, lastSeen := range activeClients {
			if lastSeen.Before(cutoff) {
				delete(activeClients, id)
			}
		}
		log.Printf("后台清理：当前跟踪 %d 个客户端", len(activeClients))
		clientMutex.Unlock()
	}
}
