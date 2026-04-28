package oidc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
)

// sessionKiller 中止某个 UID 的所有会话(Web/PC/APP)并踢 WuKongIM 长连接。
//
// 生产实现包 *config.Context.QuitUserDevice(uid, -1):清 token Redis +
// 重签 IM token 触发 WuKongIM 端 transport 失效。
type sessionKiller interface {
	Kick(ctx context.Context, uid string) error
}

// refresher 调 IdP /oauth/token grant_type=refresh_token。
//
// 抽象出来让 worker 测试可以注入 mock,无需起 httptest server,
// 也方便 isInvalidGrant 的错误分支用纯 Go 错误对象注入。
type refresher interface {
	Refresh(ctx context.Context, refreshToken string) (*RefreshResult, error)
}

// RefreshResult Refresh 成功返回的最小子集。
type RefreshResult struct {
	RefreshToken string
	ExpiresAt    time.Time
}

// syncStore SyncWorker 对持久层的最小依赖。
type syncStore interface {
	DueRefreshes(limit int) ([]*DueRefresh, error)
	// MarkRefreshRevoked 返回 rowsAffected:多实例并发时,只有一个 worker 拿到 1。
	MarkRefreshRevoked(id int64) (int64, error)
	RotateRefresh(oldID int64, newRT *RefreshModel) error
}

// DueRefresh 待刷新 RT 与所属 identity 的 uid 联合查询结果。
//
// uid 必须随 RT 一起取出,否则 invalid_grant 时无法定位要踢谁。
type DueRefresh struct {
	ID              int64
	IdentityID      int64
	UID             string
	TokenCiphertext []byte
	ExpiresAt       time.Time
}

// SyncWorkerConfig SyncWorker 调度参数。
type SyncWorkerConfig struct {
	Interval    time.Duration // ≤ 0 视为禁用,Start 直接返回
	Concurrency int           // 单批内并发刷新数量上限
	BatchSize   int           // 单 tick 拉取条数

	// LockKey / LockTTL 控制多实例 tick 级互斥(详见 sync_lock.go)。
	// 仅当 worker 注入了非 nil 的 tickLock 时生效。
	// LockTTL 默认 = Interval:lock 持有期不超过一个 tick 周期,
	// 实例崩溃 → TTL 自动到期 → 下个 tick 自然恢复。
	LockKey string
	LockTTL time.Duration
}

// SyncWorker 周期性 refresh active RT,失败即吊销 + 踢线 + 审计。
type SyncWorker struct {
	cfg    SyncWorkerConfig
	store  syncStore
	enc    *Encryptor
	rfsh   refresher
	killer sessionKiller
	audit  auditWriter
	lock   tickLock // nil = 不做多实例互斥(单实例部署 / 测试)
	log.Log

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewSyncWorker 构造 worker,并发 / 批大小 / 锁 TTL 有兜底默认值。
//
// lock 传 nil 时退化为"每实例独立 tick" —— rowsAffected 竞态检测仍能保证
// 不出假阳性踢线,只是 IdP 流量翻 N 倍。生产部署建议注入 RedisTickLock。
func NewSyncWorker(cfg SyncWorkerConfig, store syncStore, enc *Encryptor,
	rfsh refresher, killer sessionKiller, audit auditWriter, lock tickLock) *SyncWorker {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.LockKey == "" {
		cfg.LockKey = tickSyncLockKey
	}
	if cfg.LockTTL <= 0 {
		cfg.LockTTL = cfg.Interval
	}
	return &SyncWorker{
		cfg:    cfg,
		store:  store,
		enc:    enc,
		rfsh:   rfsh,
		killer: killer,
		audit:  audit,
		lock:   lock,
		Log:    log.NewTLog("OIDC-Sync"),
	}
}

// Start 启动后台 ticker goroutine。Interval ≤ 0 视为禁用。
//
// 真幂等:重复调用会先 Stop 旧 goroutine 再启动新的,避免泄漏。
// 生产路径只在模块 Init 中调一次,这里的幂等是防误用(测试 / 配置热更等)。
func (w *SyncWorker) Start(ctx context.Context) {
	if w.cfg.Interval <= 0 {
		return
	}
	// 已有运行中的 goroutine 时先优雅停掉,杜绝 cancel 被覆盖后旧 ctx 失联的泄漏。
	if w.cancel != nil {
		w.cancel()
		w.wg.Wait()
	}
	rctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		t := time.NewTicker(w.cfg.Interval)
		defer t.Stop()
		for {
			select {
			case <-rctx.Done():
				return
			case <-t.C:
				if err := w.RunOnce(rctx); err != nil && !errors.Is(err, context.Canceled) {
					w.Error("OIDC sync 轮询失败", zap.Error(err))
				}
			}
		}
	}()
}

// Stop 通知 worker 退出并等待所有进行中的刷新完成。
func (w *SyncWorker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
}

// RunOnce 执行一次同步:拉一批 → 受 Concurrency 限的并发处理。
//
// 多实例部署:抢到 tick lock 才跑,抢不到直接返回 nil(本 tick 由别人跑)。
// lock 不可用时降级为"每实例独立跑",依赖 rowsAffected 竞态检测兜底。
//
// ctx 取消时已派发的 goroutine 仍会跑完当前 RT(processOne 内部 best-effort),
// 但不再派发新任务。返回 ctx.Err() 让上层日志可识别 "正常停机" vs "异常失败"。
func (w *SyncWorker) RunOnce(ctx context.Context) error {
	if w.lock != nil {
		token, err := NewRandomString(16)
		if err != nil {
			return fmt.Errorf("oidc sync: gen lock token: %w", err)
		}
		got, err := w.lock.Acquire(ctx, w.cfg.LockKey, token, w.cfg.LockTTL)
		if err != nil {
			// Redis 故障:降级到"无锁"路径而非阻塞。打 warn 让运维注意,
			// rowsAffected 竞态检测在 DB 层兜底,不会出假阳性踢线。
			w.Warn("OIDC sync lock 故障,降级单 tick 无锁运行", zap.Error(err))
		} else if !got {
			w.Debug("OIDC sync lock 被另一实例持有,本 tick 跳过",
				zap.String("key", w.cfg.LockKey))
			return nil
		} else {
			// 成功抢锁:无论 RunOnce 走到哪个分支(panic 也保)都释放。
			//
			// Release 用 context.Background() 而非外层 ctx — Stop() 触发时
			// 外层 ctx 已 cancel,若未来 tickLock 实现尊重 context(如 etcd /
			// go-redis v8+),defer Release 会立刻被 cancel 拒掉,锁卡到 TTL
			// 自然过期。释放是 cleanup 路径,不应被取消语义连带杀掉。
			defer func() {
				if _, rerr := w.lock.Release(context.Background(), w.cfg.LockKey, token); rerr != nil {
					w.Warn("OIDC sync lock 释放失败,等 TTL 自然过期",
						zap.Error(rerr), zap.String("key", w.cfg.LockKey))
				}
			}()
		}
	}
	due, err := w.store.DueRefreshes(w.cfg.BatchSize)
	if err != nil {
		return fmt.Errorf("oidc sync: query due: %w", err)
	}
	if len(due) == 0 {
		return nil
	}
	sem := make(chan struct{}, w.cfg.Concurrency)
	var wg sync.WaitGroup
	for _, d := range due {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(rec *DueRefresh) {
			defer wg.Done()
			defer func() { <-sem }()
			// 单条 RT 的 panic 不应炸掉整个 worker 进程。捕获 + 日志 + 审计,
			// 让其他 RT 正常处理完;锁也能正常 defer release 不卡死下个 tick。
			defer func() {
				if r := recover(); r != nil {
					w.Error("OIDC sync processOne panic recovered",
						zap.Any("panic", r), zap.Int64("rt_id", rec.ID))
					w.writeAudit(rec.UID, EventRefreshFail, fmt.Sprintf("panic: %v", r))
				}
			}()
			w.processOne(ctx, rec)
		}(d)
	}
	wg.Wait()
	return nil
}

// processOne 单条 RT 的处理:解密 → refresh → 成功 rotate / invalid_grant 吊销+踢线 / 暂时错只重试。
//
// 关键决策 — 暂时性错误(网络/5xx)不踢线:
//
//	IdP 抖动期间踢全员等于自伤,所以只 audit refresh_fail 等下个 tick。
//	只有 invalid_grant 这种 IdP 主动否决的语义,才视为账号状态变更必须吊销 + 踢线。
func (w *SyncWorker) processOne(ctx context.Context, d *DueRefresh) {
	rt, err := w.enc.Decrypt(d.TokenCiphertext)
	if err != nil {
		// 密钥轮换 / 数据损坏。再放着只会反复触发解密失败 → 吊销避免占住调度位。
		// 不踢线:解密失败不是 IdP 端账号状态变化,踢用户会让密钥事故扩散成全员登出。
		w.Error("解密 RT 失败,标记吊销避免反复处理", zap.Error(err), zap.Int64("rt_id", d.ID))
		// 区分 "解密失败,已吊销" vs "解密失败 + DB 也挂了,下轮还会再来",
		// 否则运维只看到反复出现的解密 error 日志,无法定位真因。
		if _, rerr := w.store.MarkRefreshRevoked(d.ID); rerr != nil {
			w.Warn("解密失败后吊销也失败,本 RT 下轮仍会重试",
				zap.Error(rerr), zap.Int64("rt_id", d.ID))
		}
		w.writeAudit(d.UID, EventRefreshFail, "decrypt: "+err.Error())
		return
	}
	res, err := w.rfsh.Refresh(ctx, string(rt))
	if err != nil {
		invalid, fellThrough := isInvalidGrant(err)
		if invalid {
			if fellThrough {
				// 字串兜底命中,意味着 *oauth2.RetrieveError 链被某层 wrap 打断。
				// 是踢用户的安全敏感判定,上游修好之前要让运维能在日志里发现这条路径。
				w.Warn("isInvalidGrant 走字串兜底匹配,errors.As 未命中 RetrieveError —— "+
					"上游 oauth2 错误链可能被 wrap 打断,确认无误后再修",
					zap.Error(err), zap.String("uid", d.UID), zap.Int64("rt_id", d.ID))
			}
			// 多实例竞态防御:rowsAffected==0 表示另一 worker 已经把这条 RT
			// 标记吊销了 —— 通常因为它先成功 rotate 了,IdP 端旧 RT 自然失效,
			// 我们这边收到的 invalid_grant 只是旋转后的副产品,绝不能踢用户。
			//
			// 业务踢线只在 "我抢到了吊销权"(rowsAffected=1)+ "IdP 主动否决"
			// 两个条件同时成立时触发,杜绝多实例假阳性。
			affected, e := w.store.MarkRefreshRevoked(d.ID)
			if e != nil {
				w.Error("标记 RT 吊销失败", zap.Error(e), zap.Int64("rt_id", d.ID))
				w.writeAudit(d.UID, EventRefreshFail, "revoke db error: "+e.Error())
				return
			}
			if affected == 0 {
				w.Debug("invalid_grant 但 RT 已被其他 worker 吊销,视为 IdP 旋转副产品,不踢线",
					zap.String("uid", d.UID), zap.Int64("rt_id", d.ID))
				return
			}
			if e := w.killer.Kick(ctx, d.UID); e != nil {
				w.Error("OIDC 吊销后踢线失败", zap.Error(e), zap.String("uid", d.UID))
			}
			w.writeAudit(d.UID, EventRefreshFail, "invalid_grant")
			return
		}
		w.Warn("OIDC refresh 暂时性失败,下轮重试",
			zap.Error(err), zap.String("uid", d.UID), zap.Int64("rt_id", d.ID))
		w.writeAudit(d.UID, EventRefreshFail, "transient: "+err.Error())
		return
	}
	ct, err := w.enc.Encrypt([]byte(res.RefreshToken))
	if err != nil {
		w.Error("加密新 RT 失败", zap.Error(err))
		w.writeAudit(d.UID, EventRefreshFail, "encrypt: "+err.Error())
		return
	}
	newRT := &RefreshModel{
		IdentityID:      d.IdentityID,
		TokenHash:       w.enc.HashToken(res.RefreshToken),
		TokenCiphertext: ct,
		ExpiresAt:       res.ExpiresAt,
	}
	if err := w.store.RotateRefresh(d.ID, newRT); err != nil {
		// ErrAlreadyRevoked 是另一 worker 抢先轮换,正常竞态,不必告警
		if errors.Is(err, ErrAlreadyRevoked) {
			return
		}
		w.Warn("RotateRefresh 失败", zap.Error(err), zap.Int64("rt_id", d.ID))
		w.writeAudit(d.UID, EventRefreshFail, "rotate: "+err.Error())
		return
	}
	w.writeAudit(d.UID, EventRefreshOK, "")
}

// writeAudit best-effort 审计;失败仅日志,不影响主流程。
func (w *SyncWorker) writeAudit(uid string, event AuditEvent, reason string) {
	if w.audit == nil {
		return
	}
	if err := w.audit.InsertAudit(&AuditModel{UID: uid, Event: event, Reason: reason}); err != nil {
		w.Error("写 OIDC sync 审计失败", zap.Error(err), zap.String("event", string(event)))
	}
}

// isInvalidGrant 检查 oauth2 错误链是否为 IdP 主动拒绝(RT 永久失效)。
//
// 匹配两层:
//  1. *oauth2.RetrieveError.ErrorCode == "invalid_grant"(标准路径)
//  2. err.Error() 包含 "invalid_grant" 字串(兜底,适配 wrap 后 RetrieveError
//     被 fmt.Errorf %w 套层后部分客户端栈未实现 errors.As 链路的情况)
//
// 同时返回 fellThrough,标识本次判定是否走了字串兜底分支。该信息让 worker
// 在踢线前打 warn:字串兜底是安全敏感的 fallback,如果命中代表上游 wrap
// 把 RetrieveError 的链路打断了,运维需要知道。
func isInvalidGrant(err error) (bool, bool) {
	if err == nil {
		return false, false
	}
	var rerr *oauth2.RetrieveError
	if errors.As(err, &rerr) && rerr.ErrorCode == "invalid_grant" {
		return true, false
	}
	if strings.Contains(err.Error(), "invalid_grant") {
		return true, true
	}
	return false, false
}

// clientRefresher 把 *Client.Refresh 适配到 refresher 接口。
type clientRefresher struct{ c *Client }

func (cr clientRefresher) Refresh(ctx context.Context, rt string) (*RefreshResult, error) {
	tok, err := cr.c.Refresh(ctx, rt)
	if err != nil {
		return nil, err
	}
	return &RefreshResult{RefreshToken: tok.RefreshToken, ExpiresAt: tok.Expiry}, nil
}
