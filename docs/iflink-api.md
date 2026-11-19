# IF.Link API 调用文档

更新：2026-11-19（Asia/Shanghai）

## 0. 通用约定

| 项 | 值 |
| --- | --- |
| 地址 | `https://token.iflink.tech` |
| 认证 | `Authorization: Bearer <API Key>` |
| 对话兼容认证 | 原有 `x-api-key` / `x-goog-api-key`；本轮视频和 ASR 实测使用 Bearer |
| 编码 | `application/json`；明确支持文件上传的端点才使用 `multipart/form-data` |

从安全环境变量或本机凭证管理器读取 Key，不要写入源码、提示词或共享文档。以下命令统一从 `IFLINK_API_KEY` 环境变量读取凭证；使用 `requests` 的 Python 示例需要先安装该库。

### 价格单位

沿用原文的站内记账约定：文中 $ 与控制台 ¥ 的数字按 1:1 记账，不代表实际外汇汇率。
预扣与最终结算可能不同；核对费用时，应将消费记录与同一任务的退款/补扣一起计算。

### 按任务类型选择接口

| 类型 | 提交入口 | 响应方式 |
| --- | --- | --- |
| 对话 | `POST /v1/chat/completions` | 同步；`stream=true` 时使用 SSE |
| 图像 | `POST /v1/images/generations` | 同步等待图像结果 |
| 视频 | `POST /v2/video_generation` | MiniMax V2 异步任务，见第 3 节 |
| ASR/TTS/音乐 | `POST /tokenhub-media/v1/audio/generations` | 按模型及模式区分，见第 4 节 |
| 3D | `POST /tokenhub-media/v1/3d/generations` | 异步任务，见第 5 节 |

MiniMax V2 在本文中指视频协议，不是音频协议版本。录音转写、语音合成与音乐不改到视频端点。
视频提交返回 task_id；MiniMax V2 查询返回 task.id 与小写 task.status。
通用任务接口 /v1/tasks/{id} 使用大写状态；不要混用两套查询结果的解析规则。

### 视频三步

1. `POST /v2/video_generation`
2. `GET /v2/query/video_generation/{task_id}`
3. `GET /v1/videos/{task_id}/content`

三步都携带 Authorization。第三步是鉴权文件下载地址，不是另一种提交协议。
音乐、3D 和异步转写仍使用各自提交端点，再通过 /v1/tasks/{id} 查询。
通用状态：NOT_START / SUBMITTED / QUEUED / IN_PROGRESS / SUCCESS / FAILURE / UNKNOWN。

### 提交与重复请求

一次逻辑任务只提交一次。客户端为任务生成并保存 Idempotency-Key，同一任务保持相同的 Key 和请求体。
收到 task_id 后只查询该任务。提交超时、断线或响应无法解析时，不应生成新 Key 自动重发；先保留请求标识并查明是否受理。
400/401/402/403 和参数/路由错误应先修正原因，不自动重试生成。
只有查询、下载是只读请求，可以在保留任务 ID 的前提下重试。

### 列出任务产物（需要认证）

```bash
curl https://token.iflink.tech/v1/tasks/task_xxx/artifacts \
  -H "Authorization: Bearer $IFLINK_API_KEY"
```

本版重新验证了七个视频模型的最小任务和三个 ASR 模型的入口可用性；其余模型的价格和参数沿用用户提供的原文，未在本轮逐项重新实测。

---

## 1. 对话模型（20 个）

### 通用调用

```http
POST https://token.iflink.tech/v1/chat/completions
```

### 请求字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `model` | string | 是 | 见下表 |
| `messages` | array | 是 | [{role, content}]，role: system/user/assistant/tool |
| `stream` | bool | 否 | true 走 SSE |
| `temperature` | number | 否 | 0–2 |
| `top_p` | number | 否 | 0–1 |
| `max_tokens` | int | 否 | 最大输出 |
| `tools` | array | 否 | function calling 定义 |
| `tool_choice` | string/obj | 否 | auto/none/required/指定函数 |
| `response_format` | obj | 否 | {"type":"json_object"} 或 json_schema |
| `reasoning_effort` | string | 否 | low/medium/high（推理模型） |
| `stream_options` | obj | 否 | {"include_usage":true} 在流末尾带用量 |

除上述外，其余字段原样透传给上游。

### 返回（非流式）

```json
{
  "id": "chatcmpl-xxx",
  "object": "chat.completion",
  "created": 1763000000,
  "model": "deepseek-v4-1-flash-260910",
  "choices": [{
    "index": 0,
    "message": {"role": "assistant", "content": "..."},
    "finish_reason": "stop"
  }],
  "usage": {
    "prompt_tokens": 18,
    "completion_tokens": 42,
    "total_tokens": 60,
    "prompt_tokens_details": {"cached_tokens": 0}
  }
}
```

返回（流式）：每行 data: {...} 的 delta 块，最后一帧带 usage，以 data: [DONE] 结束。

Anthropic / Gemini 入口（同一批模型，格式互转）：
POST /v1/messages（配 anthropic-version）、POST /v1beta/models/{model}:generateContent。

### 模型与价格

单位：$/1M tokens。「缓存读」指命中上下文缓存的输入价，「缓存写」指写入缓存的输入价。

| 模型 | 输入 | 缓存读 | 缓存写 | 输出 | 上下文 | 备注 |
| --- | --- | --- | --- | --- | --- | --- |
| `deepseek-v4.1-flash-1017` | 2 / 1 | 0.04 / 0.02 | 2 / 1 | 8 / 4 | 1M | 峰谷价，见下 |
| `deepseek-v4-1-flash-260910` | 2 / 1 | 0.04 / 0.02 | 2 / 1 | 8 / 4 | 1M | 峰谷价，见下 |
| `deepseek-v4-flash-ga-260731` | 3 | 0.10 | 3 | 9 | 1M | — |
| `deepseek-v4-pro-0813` | 3 | 0.30 | 3 | 6 | 1M | 推理更强 |
| `glm-5.2` | 8 | 2 | 8 | 28 | 1M | — |
| `glm-5.3` | 8 | 2 | 8 | 28 | 1M | 旗舰 |
| `glm-5.3-flash` | 0.8 | 0.23 | 0.8 | 2.8 | 200K | 便宜快 |
| `glm-5.3-flashx` | 2 | 0.57 | 2 | 7 | 200K | — |
| `kimi-k2.8-preview` | 6.5 | 1.7 | 6.5 | 27 | 262K | 多模态 |
| `minimax-m3` | 2.1 | 0.42 | 2.1 | 8.4 | 200K | — |
| `minimax-m3.1-flash` | 1 | 0.1 | 1 | 4 | 200K | — |
| `step-5-preview` | 7 | 0.35 | 7 | 20 | 256K | — |
| `hy4` | 3 | 0.15 | 3 | 9 | 256K | 腾讯混元 |
| `hy4-flash` | 0.25 | 0.01 | 0.25 | 1 | 256K | 最便宜 |
| `qwen3.8-flash` | 0.80 | 0.10 | 1.25 | 2.70 | 1M | 视觉理解 |
| `hunyuan-role-latest` | 2.4 | 0 | 0 | 9.6 | 32K | 角色扮演 |
| `hy-role` | 2.4 | 0 | 0 | 9.6 | 32K | 角色扮演 |
| `hy-mt2-lite` | 0.3 | 0 | 0 | 1.2 | 32K | 翻译专用 |
| `hy-mt2-plus` | 0.5 | 0 | 0 | 2.0 | 32K | 翻译专用 |
| `hy-mt2-pro` | 0.5 | 0 | 0 | 2.0 | 32K | 翻译专用 |

### 峰谷价（前一个数为峰时，后一个为谷时）

- deepseek-v4-1-flash-260910：北京时间工作日 09:00–12:00、14:00–18:00 为峰时，其余时间为谷时。
- deepseek-v4.1-flash-1017：同一时段（判断按 UTC 01–04、06–10 折算，等价于北京时间 09–12、14–18）。

谷时价格是峰时的一半，批量任务放在夜间跑能省一半。

### 计费示例

一次请求 deepseek-v4-1-flash-260910，输入 12 000 tokens（其中 8 000 命中缓存），输出 3 000 tokens，谷时：

```text
(12000-8000)×1/1e6 + 8000×0.02/1e6 + 3000×4/1e6
= 0.004 + 0.00016 + 0.012 = $0.01616
```

---

## 2. 图像模型（9 个）

### 通用调用

```http
POST https://token.iflink.tech/v1/images/generations
```

### 请求字段

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `model` | string | 见下表 |
| `prompt` | string | 文生图提示词（长度上限随模型） |
| `size` | string | 1024x1024 形式，或 2K/3K/4K/auto |
| `images` | string[] | 参考图，URL 或 data: base64 |
| `n` | int | 生成张数（仅部分模型支持 >1） |
| `aspect_ratio` | string | 16:9 等（与 size 互斥） |
| `area` | string | 1K/2K/4K（与 size 互斥） |
| `output_format` | string | jpeg/png/webp |
| `response_format` | string | url（固定） |
| watermark / aigc_watermark | bool | 是否加 AI 生成水印 |
| `sequential_image_generation` | string | auto/disabled（seedream-lite 支持批量） |
| `sequential_image_generation_options` | obj | {"max_images": n}，最多 15 |

### 返回

```json
{
  "created": 1763000000,
  "data": [
    {"url": "https://.../image.png"}
  ]
}
```

即便上游是异步任务，/v1/images/generations 也会等结果出来再一次性返回，调用方不需要轮询。

### 改图

```http
POST https://token.iflink.tech/v1/images/edits
```

字段同上，用 images 传原图（支持 multipart 的 image 文件字段）。

### 模型与价格

按尺寸计价（取宽高的较大值：≤1024 为「小」、≤2048 为「中」、>2048 为「大」）

| 模型 | 小 | 中 | 大 | 参考图加成 | 说明 |
| --- | --- | --- | --- | --- | --- |
| `wand-vega-image-lite` | $0.135 | $0.135 | $0.18 | — | 最便宜 |
| `wand-vega-image-flash` | $0.267 | $0.374 | $0.775 | — | 快 |
| `wand-vega-image-pro` | $0.95 | $0.95 | $1.71 | 超出 3 张部分每张 $0.10 | 高质量 |
| `wand-vega-image-1.5-pro` | $1.45 | $1.45 | $1.45 | 每张参考图 $0.10 | 细节最丰富 |

### 固定单价（与尺寸无关）

| 模型 | 单价 | 参考图加成 | 说明 |
| --- | --- | --- | --- |
| `seedream-image-v5.0-flash` | $0.12 / 张 | 超出 1 张部分每张 $0.02 | 每请求只出 1 张 |
| `seedream-image-v5.0-pro` | $0.60 / 张 | 超出 1 张部分每张 $0.02 | 每请求只出 1 张 |
| `seedream-image-v5.0-lite` | $0.22 / 张 | — | 仅 2K/3K/4K，一次最多 15 张 |
| `minimax-image-h3` | $1.00 / 张 | 每张 $1.00 | 预扣价，实际按上游计量结算 |
| `minimax-image-h3-highspeed` | $1.00 / 张 | 每张 $1.00 | 同上 |

### 各模型的尺寸限制

| 模型 | 允许的 size | n 上限 |
| --- | --- | --- |
| `wand-vega-image-*` | 宽x高，面积 1–20 000 000 像素，宽高比 1/16–16 | 1 |
| seedream-image-v5.0-flash / -pro | 1K/1.5K/2K，宽x高 面积 ≥921 600 | 1 |
| `seedream-image-v5.0-lite` | 仅 2K/3K/4K | 15 |
| `minimax-image-h3*` | 1024x1024 等常规尺寸，或 auto | 10 |

### 计费示例

生成 2 张 2048×2048 的 wand-vega-image-pro，带 4 张参考图：

```text
2 × $0.95 + (4-3) × $0.10 = $2.00
```

---

## 3. 视频模型（7 个）——统一 MiniMax V2

所有视频都是异步任务。七个公开模型共用同一套提交、查询和鉴权下载流程；客户端只传公开 model，不传内部上游型号或插件名。

### 方法与路径

```http
POST /v2/video_generation                          提交
GET  /v2/query/video_generation/{task_id}           查询
GET  /v1/videos/{task_id}/content                  下载 MP4
GET  /v1/tasks/{task_id}/artifacts                 列出产物（可选）
```

已有 /v1/videos 与 /v1/video/generations 兼容入口保留，但不作为这七个模型的统一接入格式。
2026-11-19 实测：通用 prompt/duration 格式发送到 /v1/videos 调用 MiniMax-H3 时，在进入上游前返回 503 product_route_policy_unavailable。
不要仅把路径换成 /v1/videos 而保留本节 JSON；新客户端直接使用本节的 V2 格式。

### 3.1 最小请求

```bash
REQUEST_ID="$(python3 -c 'import uuid; print(uuid.uuid4())')"
# 保存这个请求标识，提交失败时不要盲目更换。
curl --fail-with-body https://token.iflink.tech/v2/video_generation \
  -H "Authorization: Bearer $IFLINK_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $REQUEST_ID" \
  -d '{
    "model": "MiniMax-H3",
    "content": [{"type": "text", "text": "A red ball slowly rolls across a white table, static camera."}],
    "duration": 4,
    "resolution": "480P",
    "ratio": "16:9"
  }'
```

REQUEST_ID 应是为这一次逻辑任务生成并持久保存的唯一值，不要把示例占位符用于所有任务。

提交响应中的 task_id 是网关公开任务 ID：

```json
{"task_id": "task_xxx"}
```

保存该 ID。不要使用上游私有任务 ID，也不要根据提交响应等待视频二进制。

### 3.2 查询

```bash
curl --fail-with-body https://token.iflink.tech/v2/query/video_generation/task_xxx \
  -H "Authorization: Bearer $IFLINK_API_KEY"
```

响应示意：

```json
{
  "task": {
    "id": "task_xxx",
    "model": "MiniMax-H3",
    "status": "succeeded"
  }
}
```

读取 `task.status`：

| 状态 | 处理 |
| --- | --- |
| `queued` / `running` | 继续查询同一 ID，建议间隔 5 秒 |
| `succeeded` | 可以下载 |
| `failed` | 显示 `task.error.message`，停止查询，不自动重新生成 |
| `unknown` | 保留任务 ID，等待后续状态或人工排查，不能当作成功 |

不同承接渠道可能附加 task.content.url、分辨率、时长、用量等字段。
统一下载不要依赖这些扩展字段；使用下面的网关鉴权下载地址。

### 3.3 下载

```bash
curl --fail-with-body https://token.iflink.tech/v1/videos/task_xxx/content \
  -H "Authorization: Bearer $IFLINK_API_KEY" \
  -o out.mp4
```

MiniMax Design 自定义适配器应返回：

```javascript
return {
  status: 'done',
  outputs: [{
    fetch: {
      method: 'GET',
      path: '/v1/videos/' + encodeURIComponent(task.id) + '/content',
    },
  }],
};
```

由应用附带供应商凭证下载文件。不要把需要 Bearer 认证的路径当作无需认证的普通 URL。
不要将 Key 拼到 URL，也不要把网关 Key 发给上游返回的第三方 CDN。

### 3.4 本次已经通过的最低规格

| 模型 | 时长 | 分辨率 | 比例 | 最终净扣费 |
| --- | --- | --- | --- | --- |
| `MiniMax-H3` | 4 秒 | 480P | 16:9 | 0.24000 |
| `wan-3.0` | 4 秒 | 480P | 16:9 | 1.08000 |
| `minimax-video-h3-fast` | 5 秒 | 480P | 16:9 | 1.65000 |
| `kling-video-v4-flash` | 3 秒 | 720P | 16:9 | 1.80000 |
| `pixverse-video-v6.0` | 1 秒 | 360P | 16:9 | 0.14665 |
| `pixverse-video-c1` | 1 秒 | 360P | 16:9 | 0.17598 |
| `pixverse-video-v5.6` | 5 秒 | 360P | 16:9 | 1.02655 |

**合计：¥6.11918**

实测日期：2026-11-19。每个模型仅一个受理任务；全部完成提交、轮询、鉴权下载并在 MiniMax Design 中播放。
Kling 预扣 3.00，完成后退回 1.20，最终 1.80；表中不是每次调用的固定报价。
Wan 原文的 2 秒请求不符合当前主渠道校验：本次使用 4 秒；当前该渠道允许整数 4–15 秒。
H3 Fast 已实测 5 秒；不要根据旧文的“≤5”自行假设更短时长可用。
首尾帧、参考视频/音频、音轨开关及其他分辨率未在本轮付费验证。

### 3.5 模型参数与预估价格

以下为原有价格表及当前解析范围，用于估算。不同承接渠道的能力与价格可能更窄或不同；最终以任务结算为准。
价格单位：每输出秒。参考素材可能另计。

#### `MiniMax-H3`

- 时长：4–15 秒；分辨率：480P / 768P / 1080P。
- 主要渠道估算单价：0.06 / 0.14 / 0.28。
- 参考图超过 3 张部分每张 0.70。
- 原文备用渠道估算单价：480P 0.297；720P–768P 0.375；1080P 0.500。
- 不同承接渠道的分辨率、素材规则与价格可能不同；本次最低规格走主要渠道成功，不保证换渠道后仍同价或一定能够切换。

#### `wan-3.0`

- 当前主渠道时长：4–15 秒；分辨率：480P / 720P / 1080P / 2K / 4K。
- 估算单价：0.27 / 0.54 / 1.08 / 1.296 / 1.555。
- 参考视频另计；预扣使用保守输入时长估计，完成后按实际用量结算。

#### `minimax-video-h3-fast`

- 本次实测：5 秒；分辨率：480P / 768P。
- 估算单价：0.33 / 0.50。

#### `kling-video-v4-flash`

- 时长：3–20 秒；分辨率：720P。
- 无参考视频时按 1.00/秒预估；本次 3 秒最终净扣费 1.80。
- 有参考视频时原有预估为 0.60 × (输出时长 + max(15.5, 输出时长))。

#### `pixverse-video-v6.0`

- 时长：1–15 秒；分辨率：360P / 540P / 720P / 1080P。
- 静音估算单价：0.147 / 0.205 / 0.264 / 0.528。
- 开音轨估算单价：0.205 / 0.264 / 0.352 / 0.675。

#### `pixverse-video-c1`

- 时长：1–15 秒；分辨率同上。
- 静音估算单价：0.176 / 0.235 / 0.293 / 0.557。
- 开音轨估算单价：0.235 / 0.293 / 0.381 / 0.704。

#### `pixverse-video-v5.6`

- 时长仅 5 / 8 / 10 秒；分辨率同上；1080P 不支持 10 秒。
- 静音估算单价：0.205 / 0.205 / 0.264 / 0.440。

### 3.6 高级素材写法

纯文生的公共字段：model（string）、content（array）、duration（整数秒）、resolution（string）、ratio（string）。
提示词长度上限沿用原文：MiniMax-H3 / wan-3.0 为 8192 字符，Kling 为 2500，PixVerse 为 5000；具体仍须通过承接渠道校验。
content 是数组；文本使用 {"type":"text","text":"..."}。
其他字段按模型能力添加；不要向所有视频模型统一发送 generate_audio、options、callback_url 或水印字段。

图片项分别如下；不要将首尾帧与参考图混用：

- 首帧：`{"type":"image_url","role":"first_frame","image_url":{"url":"https://example.com/start.png"}}`
- 尾帧：`{"type":"image_url","role":"last_frame","image_url":{"url":"https://example.com/end.png"}}`
- 参考图：`{"type":"image_url","role":"reference_image","image_url":{"url":"https://example.com/ref.png"}}`

视频参考（仅支持该能力的模型）：

```json
{"type":"video_url","role":"reference_video","video_url":{"url":"https://example.com/ref.mp4"}}
```

音频参考（仅支持该能力的模型）：

```json
{"type":"audio_url","role":"reference_audio","audio_url":{"url":"https://example.com/ref.mp3"}}
```

约束：

- 首尾帧与 reference_* 素材互斥；尾帧必须同时提供首帧。
- 不能只有音频参考；素材数量上限由模型和承接渠道校验，不把“图片 10 / 视频 3 / 音频 3”当作七个模型共同保证。
- MiniMax-H3 主要渠道不支持音频参考、generate_audio、adaptive 比例；有首帧也应传明确比例。
- Kling 不支持尾帧或音频参考。
- PixVerse 当前适配不接受视频/音频参考；支持的图片模式与字段仍由模型校验。
- PixVerse 音轨开关使用 extra.generate_audio（boolean），不是顶层通用字段；本次最小请求省略它。
- 回调不作为统一能力，统一采用轮询。

### 3.7 Python 完整最小示例（只提交一次）

```python
import os
import time
import uuid
import requests

BASE = "https://token.iflink.tech"
KEY = os.environ["IFLINK_API_KEY"]
request_id = str(uuid.uuid4())
headers = {"Authorization": f"Bearer {KEY}"}
body = {
    "model": "MiniMax-H3",
    "content": [{"type": "text", "text": "A red ball slowly rolls across a white table, static camera."}],
    "duration": 4,
    "resolution": "480P",
    "ratio": "16:9",
}
# 保存 request_id；请求异常时停止，不自动再提交。
print("request_id:", request_id)
response = requests.post(
    f"{BASE}/v2/video_generation",
    headers={**headers, "Idempotency-Key": request_id},
    json=body,
    timeout=(10, 600),
)
response.raise_for_status()
submission = response.json()
task_id = submission["task_id"]
print("task_id:", task_id)

deadline = time.monotonic() + 1800
while time.monotonic() < deadline:
    response = requests.get(
        f"{BASE}/v2/query/video_generation/{task_id}",
        headers=headers, timeout=(10, 60),
    )
    response.raise_for_status()
    task = response.json()["task"]
    if task["status"] == "succeeded":
        break
    if task["status"] == "failed":
        raise RuntimeError(f"{task_id}: {task.get('error', {}).get('message', 'Video failed')}")
    time.sleep(5)
else:
    raise TimeoutError(f"Keep polling {task_id}; do not submit again")

with requests.get(
    f"{BASE}/v1/videos/{task_id}/content",
    headers=headers, timeout=(10, 600), stream=True,
) as response:
    response.raise_for_status()
    with open("out.mp4", "wb") as output:
        for chunk in response.iter_content(1024 * 1024):
            output.write(chunk)
```

---

## 4. 语音模型（12 个）

### 统一入口

12 个语音模型（转写、合成、音乐）共用同一个端点：

```http
POST https://token.iflink.tech/tokenhub-media/v1/audio/generations
```

按 model 自动分流。TTS 是同步请求；ASR 按模型及 async 参数选择同步或异步；音乐是异步任务。

### 返回（同步）

`result` 的结构随模型变化，见后续各模型示例。公共外层如下：

```json
{
  "task_id": "task_xxx",
  "status": "SUCCESS",
  "result": {},
  "artifacts": "/v1/tasks/task_xxx/artifacts"
}
```

拿到 artifacts 路径即可下载音频/字幕：

```bash
curl https://token.iflink.tech/v1/tasks/task_xxx/artifacts -H "Authorization: Bearer $IFLINK_API_KEY"
```

音频产物的 type 为 audio，封面通常为 image。ASR 字幕的 key 为 subtitles，type 可能是 file；SRT 的 mime_type 为 application/x-subrip，不要只按 type=subtitles 筛选。

---

### 4.1 语音转文字（3 个）

**现在需要可用的 ASR，请使用 `hy-asr-3.0-preview` 的同步模式。**

#### 当前可用性：2026-11-19 真实请求

同一段 0.719875 秒、16 kHz 单声道 WAV，经公网统一音频入口各提交一次：

| 模型 | 本轮模式 | 实测结果 | 净扣费 |
| --- | --- | --- | ---: |
| `hy-asr-3.0-preview` | 同步 | HTTP 200、`SUCCESS`；返回“好。”；SRT 下载 HTTP 200 | ¥0.00015 |
| `wand-asr-v1` | 同步 | HTTP 402，上游 `401007`：没有免费额度且未开通后付费 | ¥0 |
| `tenlai-asr-v1.0` | 异步提交 | HTTP 402，上游 `401007`：没有免费额度且未开通后付费 | ¥0 |

WAND 和 Tenlai 的请求已到达上游，但当前上游账户没有开通服务。需由上游账户管理员开通相应服务的后付费，再验证成功任务；本轮没有变更上游计费设置。

Hy 的异步模式已适配；本轮只重新验证了同步模式。

#### 请求字段按模式区分

| 字段 | 类型 | 使用条件 |
| --- | --- | --- |
| `model` | string | 三个公开模型 ID 之一 |
| `data` | string | 音频文件原始字节的 base64，单次不超过 900000 字符；Hy/WAND 同步及 Tenlai 使用；不是 `data:` URL |
| `input_url` | string | 可供上游读取的音频 URL；与 `data` 二选一；Hy/WAND 异步必须使用 URL |
| `async` | boolean | 仅 Hy/WAND 可选；默认 `false`，`true` 启用异步；Tenlai 总是异步，不传此字段 |
| `source` | string | Hy/WAND 可选的音源标识；Tenlai 不传 |
| `voice_encode_format` | string | Hy/WAND 同步模式可选；异步不传 |
| `subtitle_format` | string | 仅 Hy/WAND 异步支持，取 `srt` 或 `vtt`；同步和 Tenlai 不传 |

异步任务仍用 `/v1/tasks/{task_id}` 查询，不使用视频 MiniMax V2 查询端点。查询状态为大写 `SUCCESS` / `FAILURE` 等；结果文件通过产物接口获取。

#### 最小同步请求：本地 WAV → 文字

设置好安全环境变量 `IFLINK_API_KEY`，将待转写音频保存为 `sample.wav`。以下 Python 示例只依赖标准库，且只提交一次：

```python
import base64
import json
import os
import urllib.request
import uuid
from pathlib import Path

request_id = str(uuid.uuid4())
print("request_id:", request_id)  # 保存；请求异常时不要生成新 ID 盲目重发
body = {
    "model": "hy-asr-3.0-preview",
    "data": base64.b64encode(Path("sample.wav").read_bytes()).decode(),
}
request = urllib.request.Request(
    "https://token.iflink.tech/tokenhub-media/v1/audio/generations",
    method="POST",
    headers={
        "Authorization": "Bearer " + os.environ["IFLINK_API_KEY"],
        "Content-Type": "application/json",
        "Idempotency-Key": request_id,
    },
    data=json.dumps(body).encode(),
)
with urllib.request.urlopen(request, timeout=180) as response:
    result = json.load(response)
print(json.dumps(result, ensure_ascii=False, indent=2))
```

同步成功响应示意：

```json
{
  "task_id": "task_xxx",
  "status": "SUCCESS",
  "result": {
    "output": {
      "text": "好。",
      "subtitle_url": "https://cdn.example.com/subtitles.srt"
    }
  },
  "artifacts": "/v1/tasks/task_xxx/artifacts"
}
```

不要假定所有模式一定返回 `output.text`：异步结果可能只有 `output.subtitle_url`，此时下载字幕即可。优先使用网关鉴权产物接口，不把 Key 发给第三方 CDN。

```bash
# 列出产物
curl --fail-with-body https://token.iflink.tech/v1/tasks/task_xxx/artifacts \
  -H "Authorization: Bearer $IFLINK_API_KEY"

# 下载字幕
curl --fail-with-body https://token.iflink.tech/v1/tasks/task_xxx/artifacts/subtitles/content \
  -H "Authorization: Bearer $IFLINK_API_KEY" \
  -o transcript.srt
```

#### 费用以实际结算为准

Hy 同步按上游返回用量结算，不是固定单次收费。本轮用量为 15 个上游计量 Token，最终扣 75 quota，即 **¥0.00015**。

| 模型/模式 | 估算与结算说明 |
| --- | --- |
| Hy 同步 | 按上游返回用量结算；请求预留可高于最终费用 |
| WAND 同步 | 网关按 0.90 估算预留；上游未开通，本轮没有成功价格 |
| Hy/WAND 异步 | 提交按最长时长保守预留，完成后依据上游计量结算；Hy/WAND 上限预留时长为 10 小时 |
| Tenlai 异步 | 原文估算约 0.00022/秒，最长 5 小时；本轮上游未开通，尚未验证实际成功费用 |

异步预留额度可能明显大于短音频的最终费用；终态失败按正常退款流程处理。

---


### 4.2 文字转语音（8 个）

#### MiniMax 系列（6 个）请求字段

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `model` | string | 见下表 |
| `text` | string | 待合成文本，≤9999 字符 |
| `voice_setting` | obj | {"voice_id": "male-qn-qingse", "speed": 1.0, "volume": 1.0} |
| `timbre_weights` | array | 音色混合，1–4 项 [{voice_id, weight}]，weight 1–100 |
| `audio_setting` | obj | {"sample_rate": 24000, "bitrate": 128000, "format": "mp3"} |
| `language_boost` | string | 语种增强，如 zh/en/auto |
| `subtitle_enable` | bool | 同时返回字幕 |
| `pronunciation_dict` | obj | 自定义发音 |
| `voice_modify` | obj | 音色微调 |
| `aigc_watermark` | bool | 加水印 |

voice_setting.voice_id 与 timbre_weights 二选一（后者用于多音色融合）。
不支持流式（stream 会被强制设为 false）。

#### 返回

```json
{
  "task_id": "task_xyz",
  "status": "SUCCESS",
  "result": {
    "data": {
      "audio": "https://cdn.example.com/speech.mp3",
      "status": 2
    }
  },
  "artifacts": "/v1/tasks/task_xyz/artifacts"
}
```

#### 价格

| 模型 | 单价 | 100 字（约 300 字节）花费 |
| --- | --- | --- |
| `minimax-speech-2.8-hd` | $0.0007 / 字符 | ≈ $0.07 |
| `minimax-speech-2.6-hd` | $0.00035 / UTF-8 字节 | ≈ $0.105 |
| `minimax-speech-02-hd` | $0.00035 / UTF-8 字节 | ≈ $0.105 |
| `minimax-speech-2.8-turbo` | $0.0002 / UTF-8 字节 | ≈ $0.06 |
| `minimax-speech-2.6-turbo` | $0.0002 / UTF-8 字节 | ≈ $0.06 |
| `minimax-speech-02-turbo` | $0.0002 / UTF-8 字节 | ≈ $0.06 |

2.8-hd 按字符计（一个汉字算 1），其余按 UTF-8 字节（一个汉字 3 字节）。按表中单价，中文 2.8-hd 比 2.6-hd / 02-hd 便宜，但比 Turbo 贵。

#### `tenlai-tts-v1.0`

| 字段 | 说明 |
| --- | --- |
| `text` | ≤300 字节（约 100 汉字或 300 英文字母） |
| `voice_setting.voice_id` | 必须是纯数字的音色 ID，如 "1001" |
| `voice_setting.speed` | −2 到 6 |
| `voice_setting.volume` | −10 到 10 |
| `audio_setting.sample_rate` | 只能 8000/16000/24000 |
| `output_format` | mp3 或 pcm |

价格 $0.00025 / 字节。返回 result.data.audio_url。

#### `indextts-2`

| 字段 | 说明 |
| --- | --- |
| `text` | ≤9999 字符 |
| `voice` | 参考音频 URL，做音色克隆（必填） |
| `audio_setting.format` | wav/mp3/pcm/flac |
| `audio_setting.sample_rate` | 8000–48000 |
| `output_format` | url（默认）或 hex |

价格 $0.0003 / 字节。返回 result.output.data。

#### 示例

```bash
curl https://token.iflink.tech/tokenhub-media/v1/audio/generations \
  -H "Authorization: Bearer $IFLINK_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "minimax-speech-2.8-hd",
    "text": "你好，欢迎使用词来语音合成服务。",
    "voice_setting": {"voice_id": "male-qn-qingse", "speed": 1.0, "volume": 1.0},
    "audio_setting": {"sample_rate": 24000, "format": "mp3"},
    "subtitle_enable": false
  }'
```

---

### 4.3 音乐生成（1 个）

minimax-music-v3.0，同一个端点，但是异步任务。

#### 请求字段

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `model` | string | minimax-music-v3.0 |
| `prompt` | string | 风格与内容描述，≤2000 字符 |
| `lyrics` | string | 歌词，≤3500 字符 |
| `lyrics_optimizer` | bool | 让模型自动写词（此时 lyrics 可省略） |
| `is_instrumental` | bool | 纯音乐，无 vocals |
| `audio_setting` | obj | 音质参数 |
| `aigc_watermark` | bool | 加水印 |

规则：is_instrumental 或 lyrics_optimizer 为 true 时只校验 prompt；否则必须提供 lyrics。

价格：每次 $1.00（固定，与长度无关）。

返回：提交后返回 task_id，用任务接口轮询；完成后产物含 audio（歌曲）与 cover（封面图）。

```bash
# 1) 提交
curl https://token.iflink.tech/tokenhub-media/v1/audio/generations \
  -H "Authorization: Bearer $IFLINK_API_KEY" -H "Content-Type: application/json" \
  -d '{"model":"minimax-music-v3.0","prompt":"轻快的钢琴曲，治愈系","is_instrumental":true}'
```

```bash
# 2) 轮询
curl https://token.iflink.tech/v1/tasks/$TASK_ID -H "Authorization: Bearer $IFLINK_API_KEY"
```

```bash
# 3) 取产物
curl https://token.iflink.tech/v1/tasks/$TASK_ID/artifacts -H "Authorization: Bearer $IFLINK_API_KEY"
```

---

## 5. 3D 模型（1 个）

模型：`hy-3d-3.1`

```http
POST https://token.iflink.tech/tokenhub-media/v1/3d/generations   # 提交
GET  https://token.iflink.tech/v1/tasks/{task_id}                  # 查状态
```

### 请求字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `model` | string | 是 | hy-3d-3.1 |
| `prompt` | string | 三选一 | 文本描述，≤1024 字符 |
| `image_url` | string | 三选一 | 图片 URL（http/https） |
| `image_base64` | string | 三选一 | 图片 base64，解码后 ≤6 MiB |
| `generate_type` | string | 否 | Normal（带贴图，默认）/ Geometry（白模） |
| `enable_pbr` | bool | 否 | PBR 材质，仅 Normal 有效 |
| `face_count` | int | 否 | 面数 3000–1 500 000，指定会触发自定义面数计费 |
| `multi_view_images` | array | 否 | 多视图辅助，最多 7 张 |
| `result_format` | string | 否 | stl / usdz / fbx，默认不生成额外格式 |

prompt、image_url、image_base64 必须且只能提供一个。

multi_view_images 项

```json
{"view_type": "left", "view_image_url": "https://..."}
```

view_type 可选：left / right / back / top / bottom / left_front / right_front，不可重复。有 prompt 时不能传多视图（必须给图片）。

### 返回（提交）

```json
{"task_id": "task_xxx", "status": "QUEUED"}
```

### 返回（完成）

产物中有两类文件：

| `key` | type | 说明 |
| --- | --- | --- |
| `model-0` | file | 3D 模型下载地址 |
| `preview-0` | image | 预览图 |

```bash
curl https://token.iflink.tech/v1/tasks/task_xxx/artifacts -H "Authorization: Bearer $IFLINK_API_KEY"
# → {"task_id":"task_xxx","artifacts":[
#      {"key":"model-0","type":"file","content_url":"https://.../model.fbx"},
#      {"key":"preview-0","type":"image","content_url":"https://.../preview.png"}]}
```

下载地址为：

```http
GET https://token.iflink.tech/v1/tasks/{task_id}/artifacts/model-0/content
GET https://token.iflink.tech/v1/tasks/{task_id}/artifacts/preview-0/content
```

### 价格（每个模型）

| 项 | 单价 |
| --- | --- |
| generate_type=Normal（带贴图） | $2.40 |
| generate_type=Geometry（白模） | $1.80 |
| `enable_pbr` | +$1.20 |
| face_count 自定义 | +$1.20 |
| multi_view_images 多视图 | +$1.20 |
| result_format 额外格式 | +$0.60 |

### 计费示例

| 场景 | 价格 |
| --- | --- |
| 文本生成一个带贴图模型 | $2.40 |
| 文本生成一个白模 | $1.80 |
| 图片 + 多视图 + PBR + 自定义面数 | $2.40 + 1.20 + 1.20 + 1.20 = $6.00 |
| 上述再加 FBX 输出 | $6.00 + 0.60 = $6.60 |

Geometry 模式下 enable_pbr 会被强制关闭，不收 PBR 费用。

### 完整示例

```python
import os
import time
import requests

BASE = "https://token.iflink.tech"
KEY = os.environ["IFLINK_API_KEY"]
H = {"Authorization": f"Bearer {KEY}"}

task = requests.post(f"{BASE}/tokenhub-media/v1/3d/generations", headers=H, json={
    "model": "hy-3d-3.1",
    "prompt": "一把木质靠背椅，简约北欧风",
    "generate_type": "Normal",
    "enable_pbr": True,
    "result_format": "fbx",
}).json()

task_id = task["task_id"]
while True:
    info = requests.get(f"{BASE}/v1/tasks/{task_id}", headers=H).json()
    if info["status"] == "SUCCESS":
        break
    if info["status"] == "FAILURE":
        raise RuntimeError(info.get("fail_reason"))
    time.sleep(5)

arts = requests.get(f"{BASE}/v1/tasks/{task_id}/artifacts", headers=H).json()
for a in arts["artifacts"]:
    print(a["key"], a["type"], a["content_url"])
```

---

## 6. 通用任务接口

所有异步模型（视频、音乐、3D、异步转写）底层共用一套任务接口。一般用上面的高层入口即可；需要精细控制时可直接调用。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | /v1/tasks/{任务ID} | 查询状态 |
| `GET` | /v1/tasks/{任务ID}/artifacts | 列出产物 |
| `GET` | /v1/tasks/{任务ID}/artifacts/{artifact_key}/content | 下载单个产物 |
| `HEAD` | 同上 | 只探存在性 |

产物下载支持 artifact_key 直取，但仍须使用 Authorization 认证；不要把需要鉴权的地址当作匿名分享链接。

### 模型与插件前缀对照

部分模型走带前缀的路径，路径里的第一段是插件名：

| 插件前缀 | 用途 | 模型 |
| --- | --- | --- |
| `tokenhub-media` | 图像 / 视频 / 音频 / 3D | 见 §2 / §3 / §4.3 / §5 |
| `tencent-vod` | 腾讯 VOD 通道 | MiniMax-H3、wan-3.0 的备用通道 |

音频/3D 等原生提交路径带插件前缀；通用查询和产物使用 /v1/tasks/{id}。视频客户端使用第 3 节的 MiniMax V2 提交、查询及网关视频下载路径。

---

## 7. 限制与错误

### 限制

| 项 | 值 |
| --- | --- |
| 速率 | 每 3 分钟 5000 次（按 key） |
| 请求体 | 32 MB |
| 流式超时 | 600 秒 |
| 自动重试 | 不要对生成提交统一自动重试；先判断是否已受理，保留 Idempotency-Key 和 task_id；查询/下载可只读重试 |
| 图片参考 | 各模型 1–30 张不等，见 §2 的尺寸限制表 |
| 音频 data | 单次 ≤900 000 字符，超出请用 input_url |
| 3D base64 | 解码后 ≤6 MiB |

### 错误响应

```json
{
  "error": {
    "message": "…",
    "type": "invalid_request_error",
    "code": "…"
  }
}
```

| `HTTP` | 含义 | 处理 |
| --- | --- | --- |
| 400 | 参数错误 | 看 message，通常是字段名或取值越界 |
| 401 | key 无效或已停用 | 检查 Authorization |
| 402 | 余额/套餐不足，或上游服务未开通 | 区分站内余额与上游 401007；后者需在上游开通后付费，站内充值不能解决 |
| 403 | 模型不在该 key 的可用范围 | 换模型或申请权限 |
| 404 | 路径或任务不存在 | 检查端点拼写 |
| 429 | 触限 | 查询/下载可退避重试；生成提交先确认是否已受理 |
| 501 | 该端点未实现 | 换用支持的端点 |
| 502/503 | 上游故障 | 保留请求标识和 task_id；先确认受理状态，不要盲目重新生成 |

### 常见问答

**Q：图片为什么不返回 base64？**

response_format 固定为 url，图片以可下载链接返回。

**Q：视频失败会扣钱吗？**

失败任务不结算。异步任务提交时会预扣，结束后按实际结算。

**Q：同一模型在 OpenAI / Anthropic / Gemini 三个端点都能用吗？**

对支持这些协议的对话模型可以；不要把这条结论套用于图像、视频、ASR/TTS、音乐或 3D。各类模型使用本文对应入口。

**Q：怎么查花了多少？**

控制台「日志」页每笔都有 token/张数/秒数明细与扣费。
