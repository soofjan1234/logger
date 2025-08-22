package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/natefinch/lumberjack"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"io"
	"os"
	"path"
	"reflect"
	"runtime"
	"strings"
	"time"
)

var SugarLogger *zap.SugaredLogger

func InitLogger(filename string, maxSize, maxBackups, maxAge int, compress bool) {
	// 确保目录存在且具有正确的权限
	if err := ensureDir(filename); err != nil {
		SugarLogger.Errorf("日志文件创建失败: %v", err)
	}

	writeSyncer := getLogWriter(filename, maxSize, maxBackups, maxAge, compress)
	consoleWriteSyncer := zapcore.AddSync(os.Stdout)
	encoder := getEncoder()
	// 创建分级写入器（Tee），日志将同时写入文件和控制台
	core := zapcore.NewTee(
		zapcore.NewCore(encoder, writeSyncer, zapcore.DebugLevel),
		zapcore.NewCore(encoder, consoleWriteSyncer, zapcore.DebugLevel),
	)
	logger := zap.New(core, zap.AddCaller())
	SugarLogger = logger.Sugar()
}

func ensureDir(filePath string) error {
	dir := path.Dir(filePath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return os.MkdirAll(dir, 0755)
	}
	return nil
}

func getEncoder() zapcore.Encoder {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	return zapcore.NewConsoleEncoder(encoderConfig)
}

func getLogWriter(filename string, maxSize, maxBackups, maxAge int, compress bool) zapcore.WriteSyncer {
	lumberJackLogger := &lumberjack.Logger{
		Filename:   filename,   //文件路径
		MaxSize:    maxSize,    //日志文件的最大存储量（单位MB），否则切割
		MaxBackups: maxBackups, //最多保留的文件数量
		MaxAge:     maxAge,     //旧文件最多保存的天数
		Compress:   compress,   //是否压缩
	}

	return zapcore.AddSync(lumberJackLogger)
}

// HttpLogger 请求日志切面
func HttpLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 开始计时
		start := time.Now()

		// 获取请求ID
		requestId := c.Request.Header.Get("X-Request-Id")
		// 捕获请求地址、方法、IP
		httpPath, method, clientIP := c.Request.URL.Path, c.Request.Method, c.ClientIP()
		handlerName := runtime.FuncForPC(reflect.ValueOf(c.Handler()).Pointer()).Name()

		// 解析请求参数
		var reqParams interface{}
		switch c.ContentType() {
		case "application/json":
			bodyBytes, _ := io.ReadAll(c.Request.Body)
			if len(bodyBytes) > 0 {
				var m map[string]interface{}
				if err := json.Unmarshal(bodyBytes, &m); err == nil {
					if marshal, err := json.Marshal(m); err == nil {
						reqParams = string(marshal)
					}
				}
			}
			// 恢复 Body
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		case "application/x-www-form-urlencoded", "multipart/form-data":
			_ = c.Request.ParseForm()
			reqParams = c.Request.Form
		case "application/octet-stream":
			reqParams = "[BINARY DATA]"
		default:
			// 处理 GET 请求的 query 参数
			if c.Request.Method == "GET" && len(c.Request.URL.Query()) > 0 {
				reqParams = c.Request.URL.Query()
			} else {
				reqParams = fmt.Sprintf("[UNSUPPORTED CONTENT TYPE: %s]", c.ContentType())
			}
		}

		// 记录请求日志
		requestLog := fmt.Sprintf(
			"X-Request-Id:%s\n"+
				"---------------------------请求开始-----------------------------\n"+
				"CLASS METHOD: %s\n"+
				"请求地址: %s\n"+
				"HTTP METHOD: %s\n"+
				"请求参数: %v\n"+
				"IP: %s",
			requestId, handlerName, httpPath, method, reqParams, clientIP,
		)

		// 设置响应记录器
		recorder := NewResponseRecorder(c.Writer)
		c.Writer = recorder

		// 调用后续处理
		c.Next()

		// 获取状态码和错误信息
		statusCode := recorder.Status()
		errorMessage := c.Errors.ByType(gin.ErrorTypePrivate).String()

		// 解析响应数据
		respCT := strings.SplitN(recorder.Header().Get("Content-Type"), ";", 2)[0]
		var responseData string
		switch respCT {
		case "application/json":
			responseData = recorder.Body.String()
		case "application/x-www-form-urlencoded", "multipart/form-data":
			responseData = recorder.Body.String()
		case "audio/mpeg", "text/html", "application/octet-stream":
			responseData = "[BINARY DATA]"
		default:
			responseData = fmt.Sprintf("[UNSUPPORTED CONTENT TYPE: %s]", respCT)
		}

		cost := time.Since(start)
		responseLog := fmt.Sprintf(
			"X-Request-Id:%s\n"+
				"HTTP STATUS: %d\n"+
				"Error Messages: %s\n"+
				"响应数据: %s\n"+
				"响应大小: %d\n"+
				"耗时: %s\n"+
				"---------------------------请求结束-----------------------------",
			requestId, statusCode, errorMessage, responseData, recorder.Body.Len(), cost,
		)
		SugarLogger.Infof("%s\n%s", requestLog, responseLog)
	}
}
