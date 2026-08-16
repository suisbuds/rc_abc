# API 通知系统设计

1. **question：你如何理解这个需求？**

   **answer：** 这个需求表面上是代替业务系统调用外部 HTTP API，本质上是将不稳定的外部调用从业务主链路中拆出，由独立服务承担持久化、异步投递、失败重试和状态记录。

   业务系统提交目标 URL、Header、JSON Body 和 `Idempotency-Key`。通知服务在任务持久化后返回 `202 Accepted`，随后异步投递。`202` 表示服务已经接管任务，不表示外部系统已经完成业务处理。

2. **question：当前系统的整体架构和核心链路是什么？**

   **answer：** 当前使用单个 Go 进程运行 Gin API 和固定大小的 Worker Pool，PostgreSQL 是任务状态的唯一事实来源。API 负责接收和查询，Worker 负责调度和投递。

   ```text
   当前架构
   ├── 内部业务系统
   │   ├── 广告注册系统
   │   ├── 订阅支付系统
   │   └── 商品交易系统
   │       │ POST /v1/notifications
   │       ▼
   ├── rc Go 进程
   │   ├── Gin HTTP API
   │   │   └── Notification Service
   │   ├── PostgreSQL Repository ─── PostgreSQL
   │   └── Worker Pool
   │       ├── Claim 到期任务
   │       ├── HTTP Delivery Client
   │       └── Complete 状态写回
   │           │
   │           ▼
   └── 外部供应商或下游系统
   ```

   ```text
   核心链路
   ├── 1. 鉴权并校验 Idempotency-Key、URL、Header 和 JSON Body
   ├── 2. 加密 Header，将 pending 任务写入 PostgreSQL
   ├── 3. 持久化成功后返回 202
   ├── 4. Worker 使用 FOR UPDATE SKIP LOCKED 领取任务并设置租约
   ├── 5. 领取事务提交后，在事务外执行 HTTP POST
   ├── 6. 更新 succeeded、retry_wait 或 dead
   └── 7. 调用方通过 GET /v1/notifications/:id 查询结果
   ```

3. **question：系统边界如何界定？**

   **answer：** 系统解决通用 HTTP 通知的可靠接收和传输问题，不理解 CRM、广告或库存系统的业务语义。

   ```text
   系统边界
   ├── 通知服务负责
   │   ├── 鉴权、校验和幂等创建
   │   ├── 任务持久化和 Header 加密
   │   ├── 异步 POST JSON 投递
   │   ├── 临时失败重试和 Worker 崩溃恢复
   │   └── 任务状态查询
   ├── 上游业务系统负责
   │   ├── 生成稳定事件 ID
   │   ├── 构造供应商 URL、Header 和 Body
   │   └── 业务事务与通知创建的一致性
   ├── 目标系统负责
   │   └── 根据事件 ID 实现业务幂等
   └── 明确不负责
       ├── 解析响应 Body 的业务语义
       ├── 端到端恰好一次
       ├── 供应商 Adapter、Token 刷新和动态签名
       ├── 顺序、优先级、任务依赖和前端管理后台
       ├── 自动重放 dead 任务
       └── 完整生产网络出口和多地域容灾
   ```

4. **question：API、数据模型和任务状态如何设计？**

   **answer：** API 只提供创建、查询和探针能力，避免在 MVP 中引入列表、取消、修改和人工重放的权限与审计问题。

   ```text
   HTTP API
   ├── GET /healthz
   ├── GET /readyz
   └── /v1 + Bearer Token
       ├── POST /notifications
       │   ├── 新任务：202 + Location
       │   ├── 幂等重放：200 + 原任务
       │   └── 相同 Key 不同请求：409
       └── GET /notifications/:id
   ```

   ```text
   notification_tasks
   ├── id / idempotency_key
   ├── target_url / method / encrypted headers / JSON body
   ├── status / attempt_count / next_attempt_at
   ├── lease_owner / lease_until
   ├── last_http_status / last_error
   └── created_at / updated_at
   ```

   ```text
   pending → processing → succeeded
                     ├──→ retry_wait → processing
                     └──→ dead

   processing + lease expired
   ├── attempt_count < max_attempts  → 其他 Worker 重新领取
   └── attempt_count >= max_attempts → dead
   ```

5. **question：系统如何尽可能可靠地投递，如何处理长期失败？**

   **answer：** 当前选择至少一次投递，优先避免任务丢失，同时接受极端故障窗口中可能重复投递。

   ```text
   可靠性机制
   ├── 写库成功后才返回 202
   ├── idempotency_key 唯一约束避免重复创建
   ├── SKIP LOCKED 避免多 Worker 同时领取
   ├── 租约过期恢复，Complete 校验 lease_owner
   ├── 2xx → succeeded
   ├── 网络错误、超时、408、429、5xx → 有限重试
   ├── 其他 3xx、4xx → dead
   ├── 指数退避、随机抖动和 Retry-After
   └── 耗尽尝试次数 → dead，保留最近错误
   ```

   Worker 可能在目标已经处理请求、但 `succeeded` 尚未写库时崩溃。系统必须重新投递以避免丢失，所以目标系统应按稳定事件 ID 去重，将传输层的至少一次收敛为业务效果的一次。

6. **question：关键技术选型是什么，替代方案是什么？**

   **answer：** 选型优先考虑能否用最少的状态源完成可靠性闭环，其次考虑团队熟悉度、可测试性和维护成本。

   ```text
   技术选型
   ├── Go：并发模型直接、单一二进制、团队熟悉
   │   └── 替代：Java/Kotlin、C#、Rust、Node.js、Python
   ├── Gin + Zap：基础 JSON API 和结构化日志
   │   └── 替代：net/http、Chi、Echo；slog、Zerolog
   ├── PostgreSQL：持久化、唯一约束、事务、行锁和调度共用一个状态源
   │   └── 替代：MySQL 8；更高吞吐时演进为 Outbox/CDC + 消息队列
   ├── pgx + 显式 SQL：锁、事务和条件更新直接可见
   │   └── 替代：database/sql、SQLC；不使用弱化关键 SQL 语义可见性的 ORM
   ├── Goose：版本化显式 SQL Migration
   └── AES-256-GCM：保护必须持久化的供应商 Header
   ```

7. **question：哪些 AI 建议被判断为过度设计，为什么没有采用？**

   **answer：** AI 曾建议消息队列、供应商 Adapter 平台、前端后台、自动重放、完整熔断、Kubernetes、Helm 和自动 CD。这些能力有合理场景，但会在第一版引入新状态源、权限模型、部署单元和运维范围，不是完成可靠 POST JSON 投递的必要条件。

   当前只保留能直接对应故障模型的复杂度：PostgreSQL 持久化、数据库租约、有限重试、Header 加密和真实 PostgreSQL 测试。判断标准是需求是否已经出现、故障模型是否明确、能否验证，以及是否引入新的一致性问题。

8. **question：如何验证 MVP 已经实现？**

   **answer：** 测试按领域规则、真实数据库语义和完整链路分层。Mock 只用于单元测试中隔离 Repository、时间和随机数；幂等约束、行锁、租约和完整投递使用真实 PostgreSQL 和 HTTP 请求验证。

   ```text
   验证层次
   ├── make verify：Format、Vet、Lint、Race Test 和 Build
   ├── make test-integration：真实 PostgreSQL 幂等、锁、租约和条件更新
   ├── make single-test：真实 API、Migration、PostgreSQL、Worker 和 HTTP Receiver
   ├── make all-test：默认并发创建并投递 1000 个任务
   └── GitHub Actions：独立环境复验、漏洞与密钥扫描、容器构建
   ```

   受控 Receiver 代替的只是无法在本地稳定控制的供应商边界；API、数据库、Migration、Worker 和 HTTP Client 都是真实组件。

9. **question：未来流量或复杂度明显增长时，系统如何演进？**

   **answer：** 演进由指标和实际故障驱动，而不是预先搭建全部平台能力。

   ```text
   演进路径
   ├── 安全和运营需求增加
   │   ├── Host 白名单、DNS/IP 检查和受控出口
   │   ├── 指标、告警、投递历史和 dead 任务重放
   │   └── 密钥轮换和供应商凭证 Profile
   ├── 目标之间影响明显 → 目标级并发、限流、熔断和隔离
   ├── API 与 Worker 负载差异明显 → 拆成 rc-api 和 rc-worker
   ├── 任务表增长 → 归档和时间分区
   └── PostgreSQL 调度成为瓶颈
       └── Task + Outbox 同事务
           └── Dispatcher / CDC
               └── 消息队列与分区 Worker
   ```

   引入消息队列时使用 Outbox 或 CDC，避免 API 直接双写数据库与队列。多地域阶段需要明确任务地域归属，避免不同地域同时投递同一任务。
