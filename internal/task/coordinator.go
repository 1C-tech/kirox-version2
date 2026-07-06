package task

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"reg_go/internal/core"
	"reg_go/internal/data"
	"reg_go/internal/email"
	"reg_go/internal/proxy"
	"reg_go/internal/storage"
)

// StartTaskRequest 启动任务请求
type StartTaskRequest struct {
	Count             int                              `json:"count"`
	Concurrency       int                              `json:"concurrency"`
	Delay             int                              `json:"delay"`
	OutputPath        string                           `json:"outputPath"`
	EmailProvider     string                           `json:"emailProvider"`     // "outlook" / "moemail" / "cloudmail" / "cftempemail"
	MoeMailDomains    []string                         `json:"moemailDomains"`    // 选中的域名列表
	MoeMailConfigs    map[string][]email.MoeMailConfig `json:"moemailConfigs"`    // 域名 -> 配置列表映射
	MoeMailRandomMode bool                             `json:"moemailRandomMode"` // 是否为随机模式

	CloudMailDomains    []string                            `json:"cloudmailDomains"`
	CloudMailConfigs    map[string][]email.CloudMailConfig  `json:"cloudmailConfigs"`
	CloudMailRandomMode bool                                `json:"cloudmailRandomMode"`

	CFTempEmailDomains    []string                                `json:"cftempemailDomains"`
	CFTempEmailConfigs    map[string][]email.CFTempEmailConfig    `json:"cftempemailConfigs"`
	CFTempEmailRandomMode bool                                    `json:"cftempemailRandomMode"`
}

// StartTask 公开方法（包装器）
func StartTask(req StartTaskRequest) map[string]interface{} {
	return startTask(req)
}

// startTask 启动注册任务（私有方法）
func startTask(req StartTaskRequest) map[string]interface{} {
	Manager.mu.Lock()
	if Manager.running {
		Manager.mu.Unlock()
		return map[string]interface{}{"error": "任务正在运行中"}
	}

	// 根据邮箱提供商类型处理
	emailProvider := req.EmailProvider
	if emailProvider == "" {
		emailProvider = "outlook" // 默认使用 Outlook
	}

	var outlookAccounts []email.OutlookAccount

	if emailProvider == "moemail" {
		// MoeMail 模式：验证域名和配置
		if len(req.MoeMailDomains) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "请选择至少一个域名"}
		}
		if len(req.MoeMailConfigs) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "MoeMail 配置缺失"}
		}
		// MoeMail 不需要预先加载账号，每次任务动态生成
	} else if emailProvider == "cloudmail" {
		if len(req.CloudMailDomains) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "请选择至少一个 cloud-mail 域名"}
		}
		if len(req.CloudMailConfigs) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "cloud-mail 配置缺失"}
		}
	} else if emailProvider == "cftempemail" {
		if len(req.CFTempEmailDomains) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "请选择至少一个 CF 临时邮箱域名"}
		}
		if len(req.CFTempEmailConfigs) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "CF 临时邮箱配置缺失"}
		}
	} else {
		// Outlook 模式：加载账号列表
		storedAccounts := storage.GetAccountsCached()
		if len(storedAccounts) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "请先添加微软邮箱账号"}
		}

		// 筛选未注册的账号
		for _, acc := range storedAccounts {
			registered, _ := acc["registered"].(bool)
			if !registered {
				emailAddr, _ := acc["email"].(string)
				password, _ := acc["password"].(string)
				clientID, _ := acc["clientId"].(string)
				refreshToken, _ := acc["refreshToken"].(string)

				outlookAccounts = append(outlookAccounts, email.OutlookAccount{
					Email:        emailAddr,
					Password:     password,
					ClientID:     clientID,
					RefreshToken: refreshToken,
				})
			}
		}

		if len(outlookAccounts) == 0 {
			Manager.mu.Unlock()
			return map[string]interface{}{"error": "没有可用的 Outlook 账号（所有账号已注册成功）"}
		}

		if len(outlookAccounts) < req.Count {
			Manager.mu.Unlock()
			return map[string]interface{}{
				"error": fmt.Sprintf("可用 Outlook 账号不足: 需要 %d, 仅有 %d", req.Count, len(outlookAccounts)),
			}
		}
	}

	// 初始化状态
	Manager.running = true
	Manager.stopCh = make(chan struct{})
	Manager.total = req.Count
	Manager.completed = 0
	Manager.success = 0
	Manager.failed = 0
	Manager.results = nil
	Manager.startTime = time.Now()
	Manager.mu.Unlock()

	// 清空日志
	Manager.logsMu.Lock()
	Manager.logs = nil
	Manager.logsMu.Unlock()

	// 后台执行
	go runBatch(req, emailProvider, outlookAccounts)

	return map[string]interface{}{"status": "started"}
}

// StopTask 停止任务（强制取消所有 HTTP 请求）
func StopTask(force bool) map[string]interface{} {
	Manager.mu.Lock()
	if !Manager.running {
		Manager.mu.Unlock()
		return map[string]interface{}{"error": "没有正在运行的任务"}
	}

	select {
	case <-Manager.stopCh:
	default:
		close(Manager.stopCh)
	}

	// 强制取消所有进行中的 HTTP 请求
	if Manager.cancelFunc != nil {
		Manager.cancelFunc()
	}

	Manager.running = false
	log.Println("[Kiro] 任务已强制停止，所有请求已取消")
	Manager.mu.Unlock()
	return map[string]interface{}{"status": "force_stopped"}
}

// runBatch 执行批量注册
func runBatch(req StartTaskRequest, emailProvider string, outlookAccounts []email.OutlookAccount) {
	// 创建可取消的 context，停止时立即中断所有 HTTP 请求
	taskCtx, taskCancel := context.WithCancel(context.Background())
	defer taskCancel()

	Manager.mu.Lock()
	Manager.cancelFunc = taskCancel
	Manager.mu.Unlock()

	defer func() {
		Manager.mu.Lock()
		Manager.running = false
		Manager.cancelFunc = nil
		Manager.mu.Unlock()
	}()

	outDir := req.OutputPath
	if outDir == "" {
		outDir = storage.GetResultOutputDir()
	}
	os.MkdirAll(outDir, 0755)

	taskConfig := core.NewConfig()
	taskConfig.EmailProvider = emailProvider
	taskConfig.Proxy = storage.GetProxy()
	if taskConfig.Proxy != "" {
		log.Printf("[Kiro] 已启用代理")
	}

	// 预先准备 MoeMail 域名池
	var moemailDomainPool []string
	var moemailDomainConfigs map[string][]email.MoeMailConfig
	if emailProvider == "moemail" {
		taskConfig.UseMoeMail = true
		moemailDomainPool = req.MoeMailDomains
		moemailDomainConfigs = req.MoeMailConfigs

		if len(moemailDomainPool) == 0 || len(moemailDomainConfigs) == 0 {
			log.Println("[Kiro] MoeMail 域名或配置为空，任务终止")
			Manager.mu.Lock()
			Manager.running = false
			Manager.mu.Unlock()
			return
		}

		log.Printf("[Kiro] MoeMail 域名池: %v (共 %d 个域名)", moemailDomainPool, len(moemailDomainPool))
	} else if emailProvider == "outlook" {
		taskConfig.UseOutlook = true
	}

	// 预先准备 CloudMail 域名池
	var cloudmailDomainPool []string
	var cloudmailDomainConfigs map[string][]email.CloudMailConfig
	if emailProvider == "cloudmail" {
		taskConfig.UseCloudMail = true
		cloudmailDomainPool = req.CloudMailDomains
		cloudmailDomainConfigs = req.CloudMailConfigs

		if len(cloudmailDomainPool) == 0 || len(cloudmailDomainConfigs) == 0 {
			log.Println("[Kiro] cloud-mail 域名或配置为空，任务终止")
			Manager.mu.Lock()
			Manager.running = false
			Manager.mu.Unlock()
			return
		}

		log.Printf("[Kiro] cloud-mail 域名池: %v (共 %d 个域名)", cloudmailDomainPool, len(cloudmailDomainPool))
	}

	// 预先准备 CF 临时邮箱域名池
	var cftempemailDomainPool []string
	var cftempemailDomainConfigs map[string][]email.CFTempEmailConfig
	if emailProvider == "cftempemail" {
		taskConfig.UseCFTempEmail = true
		cftempemailDomainPool = req.CFTempEmailDomains
		cftempemailDomainConfigs = req.CFTempEmailConfigs

		if len(cftempemailDomainPool) == 0 || len(cftempemailDomainConfigs) == 0 {
			log.Println("[Kiro] CF 临时邮箱域名或配置为空，任务终止")
			Manager.mu.Lock()
			Manager.running = false
			Manager.mu.Unlock()
			return
		}

		log.Printf("[Kiro] CF 临时邮箱域名池: %v (共 %d 个域名)", cftempemailDomainPool, len(cftempemailDomainPool))
	}

	// 统计计数器
	var statsMu sync.Mutex
	var taskDurations []float64
	var failRegistered, failNetwork, failBanned, failOther int
	taskStartTime := time.Now()

	// 共享账号池（并发安全），goroutine 动态领取账号（仅 Outlook 模式使用）
	var accountPoolMu sync.Mutex
	accountPoolIdx := 0
	nextAccount := func() (email.OutlookAccount, bool) {
		accountPoolMu.Lock()
		defer accountPoolMu.Unlock()
		if accountPoolIdx >= len(outlookAccounts) {
			return email.OutlookAccount{}, false
		}
		acc := outlookAccounts[accountPoolIdx]
		accountPoolIdx++
		return acc, true
	}

	// MoeMail 域名池索引（并发安全）
	var moemailDomainIdx int
	var moemailDomainMu sync.Mutex
	nextMoeMailDomain := func() (string, email.MoeMailConfig) {
		moemailDomainMu.Lock()
		defer moemailDomainMu.Unlock()

		var domain string
		if req.MoeMailRandomMode {
			domain = moemailDomainPool[rand.Intn(len(moemailDomainPool))]
		} else {
			domain = moemailDomainPool[moemailDomainIdx%len(moemailDomainPool)]
			moemailDomainIdx++
		}

		configs := moemailDomainConfigs[domain]
		return domain, configs[rand.Intn(len(configs))]
	}

	// CloudMail 域名池索引（并发安全）
	var cloudmailDomainIdx int
	var cloudmailDomainMu sync.Mutex
	nextCloudMailDomain := func() (string, email.CloudMailConfig) {
		cloudmailDomainMu.Lock()
		defer cloudmailDomainMu.Unlock()

		var domain string
		if req.CloudMailRandomMode {
			domain = cloudmailDomainPool[rand.Intn(len(cloudmailDomainPool))]
		} else {
			domain = cloudmailDomainPool[cloudmailDomainIdx%len(cloudmailDomainPool)]
			cloudmailDomainIdx++
		}

		configs := cloudmailDomainConfigs[domain]
		return domain, configs[rand.Intn(len(configs))]
	}

	// CF 临时邮箱域名池索引（并发安全）
	var cftempemailDomainIdx int
	var cftempemailDomainMu sync.Mutex
	nextCFTempEmailDomain := func() (string, email.CFTempEmailConfig) {
		cftempemailDomainMu.Lock()
		defer cftempemailDomainMu.Unlock()

		var domain string
		if req.CFTempEmailRandomMode {
			domain = cftempemailDomainPool[rand.Intn(len(cftempemailDomainPool))]
		} else {
			domain = cftempemailDomainPool[cftempemailDomainIdx%len(cftempemailDomainPool)]
			cftempemailDomainIdx++
		}

		configs := cftempemailDomainConfigs[domain]
		return domain, configs[rand.Intn(len(configs))]
	}

	// send-otp 400 熔断：任一任务遇到该错误即终止全部并发任务（只触发一次）
	var otpKillOnce sync.Once
	doTask := func(i int) {
		select {
		case <-Manager.stopCh:
			return
		default:
		}

		taskCfg := *taskConfig
		taskCfg.Password = core.GenPassword()
		// 多代理池：若存在启用项，按权重抽签覆盖单代理
		if picked := proxy.PickRandom(); picked != "" {
			taskCfg.Proxy = picked
			log.Printf("[Kiro][%d/%d] 选中代理 %s", i+1, req.Count, picked)
		}
		var currentEmail string

		// 根据邮箱提供商类型获取邮箱
		if emailProvider == "outlook" {
			// Outlook 模式：从共享池领取账号
			acc, ok := nextAccount()
			if !ok {
				log.Printf("[Kiro][%d/%d] 无可用账号，跳过", i+1, req.Count)
				Manager.mu.Lock()
				Manager.completed++
				Manager.failed++
				Manager.mu.Unlock()
				return
			}
			taskCfg.OutlookAccount = &acc
			currentEmail = acc.Email
		} else if emailProvider == "moemail" {
			// MoeMail 模式：动态生成临时邮箱
			// 从域名池中获取域名和配置
			domain, config := nextMoeMailDomain()

			// 生成完全随机的邮箱名
			emailName := email.GenerateEmailName(i)

			// 使用 1 小时有效期
			expiryTime := int64(3600000) // 1 小时（毫秒）

			log.Printf("[Kiro][%d/%d] 创建 MoeMail 邮箱: %s@%s (配置: %s)", i+1, req.Count, emailName, domain, config.Name)

			// 创建 MoeMail 提供商
			provider, err := email.NewMoeMailProvider(config, emailName, expiryTime, domain)
			if err != nil {
				log.Printf("[Kiro][%d/%d] 生成 MoeMail 邮箱失败: %v", i+1, req.Count, err)
				Manager.mu.Lock()
				Manager.completed++
				Manager.failed++
				Manager.mu.Unlock()
				return
			}

			taskCfg.MoeMailProvider = provider
			currentEmail = provider.GetAddress()
		} else if emailProvider == "cloudmail" {
			domain, config := nextCloudMailDomain()
			emailName := email.GenerateEmailName(i)

			log.Printf("[Kiro][%d/%d] 创建 cloud-mail 邮箱: %s@%s (配置: %s)", i+1, req.Count, emailName, domain, config.Name)

			provider, err := email.NewCloudMailProvider(config, emailName, domain)
			if err != nil {
				log.Printf("[Kiro][%d/%d] 生成 cloud-mail 邮箱失败: %v", i+1, req.Count, err)
				Manager.mu.Lock()
				Manager.completed++
				Manager.failed++
				Manager.mu.Unlock()
				return
			}

			taskCfg.CloudMailProvider = provider
			cfgCopy := config
			taskCfg.CloudMailConfig = &cfgCopy
			currentEmail = provider.GetAddress()
		} else if emailProvider == "cftempemail" {
			domain, config := nextCFTempEmailDomain()
			emailName := email.GenerateEmailName(i)

			log.Printf("[Kiro][%d/%d] 创建 CF 临时邮箱: %s@%s (配置: %s)", i+1, req.Count, emailName, domain, config.Name)

			provider, err := email.NewCFTempEmailProvider(config, emailName, domain)
			if err != nil {
				log.Printf("[Kiro][%d/%d] 生成 CF 临时邮箱失败: %v", i+1, req.Count, err)
				Manager.mu.Lock()
				Manager.completed++
				Manager.failed++
				Manager.mu.Unlock()
				return
			}

			taskCfg.CFTempEmailProvider = provider
			cfgCopy := config
			taskCfg.CFTempEmailConfig = &cfgCopy
			currentEmail = provider.GetAddress()
		}

		log.Printf("[Kiro][%d/%d] 开始注册", i+1, req.Count)
		itemStart := time.Now()

		const maxAttempts = 2

		var result map[string]interface{}
	retryLoop:
		for attempt := 0; attempt < maxAttempts; attempt++ {
			// 每次重试前检查停止信号
			select {
			case <-Manager.stopCh:
				return
			default:
			}

			if attempt > 0 {
				log.Printf("[Kiro][%d/%d] 第 %d 次重试", i+1, req.Count, attempt)
				select {
				case <-Manager.stopCh:
					return
				case <-time.After(time.Duration(2+attempt) * time.Second):
				}
			}

			if taskCtx.Err() != nil {
				return
			}

			reg := core.NewRegistrar(&taskCfg)
			reg.Ctx = taskCtx
			reg.TaskLabel = fmt.Sprintf("%d/%d", i+1, req.Count)
			result = reg.Run()

			if result["status"] == "success" {
				break
			}

			errorMsg, _ := result["error"].(string)

			// send-otp 400: 临时邮箱模式下优先换邮箱重试，不直接熔断
			// CPA 项目逻辑：单个邮箱/域名被标，换一个即可，不应终止全部任务
			if strings.Contains(errorMsg, "send-otp 失败 (400)") && (emailProvider == "cftempemail" || emailProvider == "moemail" || emailProvider == "cloudmail") {
				log.Printf("[Kiro][%d/%d] send-otp 400，换邮箱重试 (当前: %s)", i+1, req.Count, currentEmail)
				var newProviderCreated bool
				if emailProvider == "cftempemail" {
					domain, config := nextCFTempEmailDomain()
					emailName := email.GenerateEmailName(i*100 + attempt)
					provider, err := email.NewCFTempEmailProvider(config, emailName, domain)
					if err == nil {
						taskCfg.CFTempEmailProvider = provider
						cfgCopy := config
						taskCfg.CFTempEmailConfig = &cfgCopy
						currentEmail = provider.GetAddress()
						taskCfg.Password = core.GenPassword()
						attempt = -1
						newProviderCreated = true
						log.Printf("[Kiro][%d/%d] 新邮箱: %s，继续注册", i+1, req.Count, currentEmail)
					} else {
						log.Printf("[Kiro][%d/%d] 创建新 CF 邮箱失败: %v", i+1, req.Count, err)
					}
				} else if emailProvider == "moemail" {
					domain, config := nextMoeMailDomain()
					emailName := email.GenerateEmailName(i*100 + attempt)
					provider, err := email.NewMoeMailProvider(config, emailName, 3600000, domain)
					if err == nil {
						taskCfg.MoeMailProvider = provider
						currentEmail = provider.GetAddress()
						taskCfg.Password = core.GenPassword()
						attempt = -1
						newProviderCreated = true
						log.Printf("[Kiro][%d/%d] 新邮箱: %s，继续注册", i+1, req.Count, currentEmail)
					} else {
						log.Printf("[Kiro][%d/%d] 创建新 MoeMail 邮箱失败: %v", i+1, req.Count, err)
					}
				} else if emailProvider == "cloudmail" {
					domain, config := nextCloudMailDomain()
					emailName := email.GenerateEmailName(i*100 + attempt)
					provider, err := email.NewCloudMailProvider(config, emailName, domain)
					if err == nil {
						taskCfg.CloudMailProvider = provider
						cfgCopy := config
						taskCfg.CloudMailConfig = &cfgCopy
						currentEmail = provider.GetAddress()
						taskCfg.Password = core.GenPassword()
						attempt = -1
						newProviderCreated = true
						log.Printf("[Kiro][%d/%d] 新邮箱: %s，继续注册", i+1, req.Count, currentEmail)
					} else {
						log.Printf("[Kiro][%d/%d] 创建新 CloudMail 邮箱失败: %v", i+1, req.Count, err)
					}
				}
				if newProviderCreated {
					continue retryLoop
				}
			}

			// AWS 熔断：非临时邮箱的 400/BLOCKED/IP-flagged 类错误才终止全部
			// 触发后继续跑只会烧邮箱、烧代理额度
			if isKillSwitchError(errorMsg) {
				otpKillOnce.Do(func() {
					log.Printf("[Kiro] ⚠️ 检测到熔断级错误(%s)，立即终止所有注册任务", errorMsg)
					go StopTask(true)
				})
				break
			}

			// 邮箱已注册：标记当前账号，换号重来（重置 attempt）
			if taskConfig.UseOutlook && strings.Contains(errorMsg, "邮箱已注册过") {
				log.Printf("[Kiro][%d/%d] %s 已注册，标记并换号", i+1, req.Count, currentEmail)
				email.UpdateAccountStatus(currentEmail, true, false)
				acc, ok := nextAccount()
				if ok {
					taskCfg.OutlookAccount = &acc
					taskCfg.Password = core.GenPassword()
					currentEmail = acc.Email
					attempt = -1 // 换号：代理预算重置
					continue retryLoop
				}
				// 账号池耗尽
				log.Printf("[Kiro][%d/%d] 账号池已耗尽", i+1, req.Count)
				break
			}

			// Point of no return：Step12 已完成但整体失败 → 邮箱已消耗，不换代理重试
			if pwSet, _ := result["passwordSet"].(bool); pwSet {
				log.Printf("[Kiro][%d/%d] 密码已设置但验活失败，邮箱已消耗，不再重试", i+1, req.Count)
				break
			}

			// 不重试的错误类型（含 context 取消 / 被封 / 临时邮箱重复）
			noRetryErrors := []string{"suspended", "临时邮箱不可能已存在", "邮箱创建失败", "context canceled", "context deadline exceeded"}
			shouldRetry := true
			for _, noRetry := range noRetryErrors {
				if strings.Contains(errorMsg, noRetry) {
					shouldRetry = false
					break
				}
			}

			if !shouldRetry || attempt >= maxAttempts-1 {
				break
			}

			log.Printf("[Kiro][%d/%d] 注册失败: %s，准备重试", i+1, req.Count, errorMsg)
		}

		itemDuration := time.Since(itemStart).Seconds()

		Manager.mu.Lock()
		Manager.results = append(Manager.results, result)
		Manager.completed++

		success := result["status"] == "success"
		if success {
			Manager.success++
		} else {
			Manager.failed++
		}
		completedCount := Manager.completed
		Manager.mu.Unlock()

		// 统计分类
		statsMu.Lock()
		taskDurations = append(taskDurations, itemDuration)
		if !success {
			errorMsg, _ := result["error"].(string)
			errClass := classifyError(errorMsg)
			switch errClass {
			case "registered":
				failRegistered++
			case "banned":
				failBanned++
			default:
				if strings.Contains(errorMsg, "timeout") || strings.Contains(errorMsg, "网络") || strings.Contains(errorMsg, "connection") || strings.Contains(errorMsg, "TLS") {
					failNetwork++
				} else {
					failOther++
				}
			}
		}
		statsMu.Unlock()

		// log.Printf 必须在 state.mu 外调用，否则与 logWriter 死锁
		if !success {
			if errMsg, ok := result["error"].(string); ok {
				log.Printf("[Kiro][%d/%d] 失败: %s (%s)", completedCount, req.Count, errMsg, currentEmail)
			}
		}

		// 只有设置完密码后（passwordSet=true）才标记邮箱为已注册
		// 之前步骤失败的邮箱不标记，等同于归还到邮箱池
		if taskConfig.UseOutlook && currentEmail != "" {
			passwordSet, _ := result["passwordSet"].(bool)
			if passwordSet {
				email.UpdateAccountStatus(currentEmail, true, success)
			}
			// 未设密码的失败邮箱不标记 registered，下次任务可继续使用
		}
		if success {
			if err := data.SaveKiroSuccess(result, outDir); err != nil {
				log.Printf("[Kiro] 保存结果失败: %v", err)
			}
		}
	}

	if req.Concurrency > 1 {
		log.Printf("[Kiro] 启动并发任务: %d 个任务，并发数 %d", req.Count, req.Concurrency)
		sem := make(chan struct{}, req.Concurrency)
		var wg sync.WaitGroup
	loop:
		for i := 0; i < req.Count; i++ {
			select {
			case <-Manager.stopCh:
				break loop
			default:
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int) {
				defer wg.Done()
				defer func() { <-sem }()
				doTask(idx)
			}(i)
		}
		wg.Wait()
	} else {
		log.Printf("[Kiro] 启动串行任务: %d 个任务", req.Count)
		for i := 0; i < req.Count; i++ {
			select {
			case <-Manager.stopCh:
				log.Println("任务已停止")
				return
			default:
			}
			doTask(i)
			if req.Delay > 0 && i < req.Count-1 {
				time.Sleep(time.Duration(req.Delay) * time.Second)
			}
		}
	}

	totalDuration := time.Since(taskStartTime).Seconds()

	Manager.mu.Lock()
	sucCount := Manager.success
	failCount := Manager.failed
	totalCount := Manager.completed
	Manager.mu.Unlock()

	// 计算平均耗时
	var avgDur float64
	if len(taskDurations) > 0 {
		var sum float64
		for _, d := range taskDurations {
			sum += d
		}
		avgDur = sum / float64(len(taskDurations))
	}

	// 统计报告
	log.Println("[Kiro] ═══════════════════════════════")
	log.Printf("[Kiro] 任务完成 — 总计: %d, 成功: %d, 失败: %d", totalCount, sucCount, failCount)
	log.Printf("[Kiro] 总耗时: %.1fs, 平均耗时: %.1fs/个", totalDuration, avgDur)
	if totalCount > 0 {
		log.Printf("[Kiro] 成功率: %.1f%%", float64(sucCount)/float64(totalCount)*100)
	}
	if failCount > 0 {
		log.Printf("[Kiro] 失败明细:")
		if failRegistered > 0 {
			log.Printf("[Kiro]   邮箱已注册: %d (%.0f%%)", failRegistered, float64(failRegistered)/float64(totalCount)*100)
		}
		if failBanned > 0 {
			log.Printf("[Kiro]   账号封禁: %d (%.0f%%)", failBanned, float64(failBanned)/float64(totalCount)*100)
		}
		if failNetwork > 0 {
			log.Printf("[Kiro]   网络问题: %d (%.0f%%)", failNetwork, float64(failNetwork)/float64(totalCount)*100)
		}
		if failOther > 0 {
			log.Printf("[Kiro]   其他错误: %d (%.0f%%)", failOther, float64(failOther)/float64(totalCount)*100)
		}
	}
	if sucCount > 0 {
		log.Printf("[Kiro] 成功结果: %s", outDir)
	}
	log.Println("[Kiro] ═══════════════════════════════")
}

// classifyError 根据错误信息粗分类，用于统计展示。
func classifyError(errorMsg string) string {
	if errorMsg == "" {
		return "failed"
	}
	if strings.Contains(errorMsg, "suspended") {
		return "banned"
	}
	if strings.Contains(errorMsg, "邮箱已注册过") || strings.Contains(errorMsg, "临时邮箱不可能已存在") {
		return "registered"
	}
	return "failed"
}

// isKillSwitchError 判断该错误是否属于"AWS 已把我们拉黑，继续跑没意义"的熔断级错误。
// 命中则立即终止全部并发任务。与单纯的瞬态失败（网络超时、验证码延迟）区分。
func isKillSwitchError(errorMsg string) bool {
	if errorMsg == "" {
		return false
	}
	triggers := []string{
		"send-otp 失败 (400)",     // Step9 原始 400
		"注册被拦截",                // formatError 对 BLOCKED/注册请求被拦截 的翻译
		"IP或浏览器指纹被检测",    // 指纹/IP 被标记
		"BLOCKED",                  // 响应体里直接包含的风控标记
		"注册请求被拦截",
	}
	for _, t := range triggers {
		if strings.Contains(errorMsg, t) {
			return true
		}
	}
	return false
}
