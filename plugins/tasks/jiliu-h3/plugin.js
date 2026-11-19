export const meta = {
  apiVersion: 1,
  requiredCapabilities: ["submit-idempotency@1"],
  key: "jiliu-h3",
  name: "基流 H3",
  icon: "text:H3",
  description: {
    en: "Jiliu H3 video generation from text, reference media, or keyframes",
    zh: "基流 H3 文生视频、参考素材及首尾帧生成视频",
  },
  version: "1.2.0",
  author: { name: "IF.Link" },
  baseUrl: "https://api.jiliu.cn",
  auth: "api_key",
  models: ["MiniMax-H3"],
  fetchMode: "per_task",
  upstreams: ["vendor", "new_api"],
  protocols: ["openai_video", "minimax_video"],
  usageSchema: {
    seconds: {
      type: "number",
      unit: "second",
      description: { en: "Video generation unit price", zh: "视频生成单价" },
    },
    resolution: {
      enum: ["480P", "768P", "1080P", "other"],
      description: { en: "Output video resolution", zh: "输出视频分辨率" },
      enumLabels: { other: { en: "Other resolution", zh: "其他分辨率" } },
    },
    task: {
      enum: ["t2va", "ref2va", "fl2va"],
      description: { en: "Generate videos", zh: "生成视频" },
      enumLabels: {
        t2va: { en: "Text to video", zh: "文生视频" },
        ref2va: { en: "Reference media to video", zh: "参考素材生成视频" },
        fl2va: { en: "Keyframes to video", zh: "首尾帧生成视频" },
      },
    },
    input_images: {
      type: "number",
      unit: "count",
      unitLabel: { en: "image", zh: "张" },
      description: { en: "Input image unit price", zh: "输入图片单价" },
    },
    input_video_seconds: {
      type: "number",
      unit: "second",
      description: { en: "Input video unit price", zh: "输入视频单价" },
    },
  },
  usageExamples: [
    {
      label: "T2VA · 5s · 768P",
      facts: { seconds: 5, resolution: "768P", task: "t2va", input_images: 0, input_video_seconds: 0 },
    },
    {
      label: "Ref2VA · 4s · 480P · 4s reference",
      facts: { seconds: 4, resolution: "480P", task: "ref2va", input_images: 0, input_video_seconds: 4 },
    },
  ],
};

const ACTIONS = {
  t2va: "text_to_video",
  ref2va: "reference_to_video",
  fl2va: "first_tail_to_video",
};

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function videoRequest(value, model) {
  if (!isObject(value)) throw new Error("JSON object required");
  if (!Object.prototype.hasOwnProperty.call(ACTIONS, value.task)) throw new Error("task must be t2va, ref2va, or fl2va");
  if (typeof value.prompt !== "string" || !value.prompt.trim()) throw new Error("prompt is required");
  const target = value.target;
  if (!isObject(target)) throw new Error("target is required");
  // Jiliu's API rejects durations outside this provider-specific range.
  if (!Number.isInteger(target.duration_seconds) || target.duration_seconds < 4 || target.duration_seconds > 15) {
    throw new Error("target.duration_seconds must be an integer between 4 and 15");
  }
  if (!Number.isSafeInteger(target.short_edge) || target.short_edge <= 0) throw new Error("target.short_edge must be a positive safe integer");
  if (typeof target.aspect_ratio !== "string" || !/^[1-9]\d*:[1-9]\d*$/.test(target.aspect_ratio)) {
    throw new Error("target.aspect_ratio must be a positive width:height ratio");
  }
  const conditions = value.conditions === undefined ? [] : value.conditions;
  if (!Array.isArray(conditions)) throw new Error("conditions must be an array");
  if (value.task === "t2va" && conditions.length) throw new Error("t2va does not accept reference conditions");
  if (value.task !== "t2va" && !conditions.length) throw new Error("conditions are required for ref2va and fl2va");
  const frames = new Set();
  for (const condition of conditions) {
    if (!isObject(condition)) throw new Error("each condition must be an object");
    if (typeof condition.uri !== "string" || !/^https?:\/\/[^\s/?#]+(?:[^\s]*)$/.test(condition.uri)) {
      throw new Error("condition.uri must be an absolute HTTP or HTTPS URL");
    }
    if (value.task === "ref2va") {
      if (!["image", "video"].includes(condition.type) || condition.role !== "reference" || condition.frame_index !== undefined) {
        throw new Error("ref2va conditions must be image or video references without frame_index");
      }
    } else if (value.task === "fl2va") {
      if (condition.type !== "image" || condition.role !== "keyframe" || ![0, -1].includes(condition.frame_index)) {
        throw new Error("fl2va conditions must be image keyframes with frame_index 0 or -1");
      }
      if (frames.has(condition.frame_index)) throw new Error("keyframe indices must be unique");
      frames.add(condition.frame_index);
    }
  }
  if (value.task === "fl2va" && !frames.has(0)) throw new Error("fl2va requires a first frame with frame_index 0");
  const request = {
    model: model,
    task: value.task,
    prompt: value.prompt,
    target: {
      duration_seconds: target.duration_seconds,
      short_edge: target.short_edge,
      aspect_ratio: target.aspect_ratio,
    },
  };
  if (conditions.length)
    request.conditions = conditions.map(function (condition) {
      return Object.assign({}, condition);
    });
  return request;
}

// Normalize the supported MiniMax V2 fields into the existing Jiliu request.
// All submission, metering and settlement hooks still consume that one shape.
function officialRequest(value) {
  if (!isObject(value)) throw new Error("JSON object required");
  const allowed = ["model", "content", "resolution", "duration", "ratio", "callback_url", "aigc_watermark"];
  for (const key of Object.keys(value)) {
    if (!allowed.includes(key)) throw new Error("Jiliu H3 does not support the field: " + key);
  }
  if (value.model !== "MiniMax-H3") throw new Error("model must be MiniMax-H3");
  if (value.callback_url !== undefined && value.callback_url !== "") throw new Error("Jiliu H3 does not support callback_url; poll the task instead");
  if (value.aigc_watermark !== undefined && value.aigc_watermark !== false) throw new Error("Jiliu H3 does not support aigc_watermark");
  if (!["480P", "768P", "1080P"].includes(value.resolution)) {
    throw new Error("Jiliu H3 resolution must be 480P, 768P or 1080P; 2K is not supported");
  }
  if (!["21:9", "16:9", "4:3", "1:1", "3:4", "9:16"].includes(value.ratio)) {
    throw new Error("Jiliu H3 requires an explicit ratio; adaptive is not supported");
  }
  if (!Array.isArray(value.content) || !value.content.length) throw new Error("content must be a non-empty array");
  const text = [];
  const conditions = [];
  let hasFrames = false;
  let hasReferences = false;
  let referenceImages = 0;
  let referenceVideos = 0;
  for (const item of value.content) {
    if (!isObject(item)) throw new Error("each content item must be an object");
    if (item.type === "text") {
      if (
        Object.keys(item).some(function (key) {
          return key !== "type" && key !== "text";
        })
      )
        throw new Error("unsupported text content field");
      if (typeof item.text !== "string" || !item.text.trim()) throw new Error("text content must be non-empty");
      text.push(item.text);
      continue;
    }
    if (item.type === "audio_url") throw new Error("Jiliu H3 does not support audio references");
    if (item.type !== "image_url" && item.type !== "video_url") throw new Error("unsupported content type");
    if (
      Object.keys(item).some(function (key) {
        return key !== "type" && key !== "role" && key !== item.type;
      })
    )
      throw new Error("unsupported media content field");
    const media = item[item.type];
    if (
      !isObject(media) ||
      Object.keys(media).some(function (key) {
        return key !== "url";
      })
    )
      throw new Error("media must contain a url only");
    const role = item.role === undefined ? (item.type === "image_url" ? "first_frame" : "reference_video") : item.role;
    if (item.type === "image_url" && (role === "first_frame" || role === "last_frame")) {
      hasFrames = true;
      conditions.push({ type: "image", role: "keyframe", frame_index: role === "first_frame" ? 0 : -1, uri: media.url });
    } else if (item.type === "image_url" && role === "reference_image") {
      hasReferences = true;
      referenceImages++;
      conditions.push({ type: "image", role: "reference", uri: media.url });
    } else if (item.type === "video_url" && role === "reference_video") {
      hasReferences = true;
      referenceVideos++;
      conditions.push({ type: "video", role: "reference", uri: media.url });
    } else {
      throw new Error("unsupported media role");
    }
  }
  if (!text.length) throw new Error("content requires a non-empty text item");
  if (hasFrames && hasReferences) throw new Error("frame images cannot be mixed with reference media");
  if (referenceImages > 9 || referenceVideos > 3) throw new Error("at most 9 reference images and 3 reference videos are supported");
  const request = {
    model: value.model,
    task: hasFrames ? "fl2va" : hasReferences ? "ref2va" : "t2va",
    prompt: text.join("\n"),
    target: { duration_seconds: value.duration, short_edge: Number(value.resolution.slice(0, -1)), aspect_ratio: value.ratio },
  };
  if (conditions.length) request.conditions = conditions;
  return videoRequest(request, value.model);
}

export const native = {
  createTask: function (ctx) {
    if (!ctx.body || ctx.body.kind !== "json") throw new Error("Jiliu H3 requires a JSON request body");
    const request = officialRequest(ctx.body.value);
    return { kind: "submit", model: request.model, action: ACTIONS[request.task], requestBody: request };
  },
  taskCreated: function (_ctx, task) {
    return { task_id: task.task_id };
  },
  taskStatus: function (ctx, task) {
    const data = isObject(task.data) ? task.data : {};
    const statuses = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "running", SUCCESS: "succeeded", FAILURE: "failed" };
    const result = {
      id: task.task_id,
      model: ctx.model || "MiniMax-H3",
      status: Object.prototype.hasOwnProperty.call(statuses, task.status) ? statuses[task.status] : "unknown",
      created_at: task.created_at,
      updated_at: task.updated_at || task.created_at,
      task_type: "generation",
      modality: "video",
    };
    if (task.status === "SUCCESS") {
      const usage = completedUsage({}, data);
      result.duration = usage.seconds;
      if (usage.resolution) result.resolution = usage.resolution;
      result.usage = { output_seconds: usage.seconds, input_seconds: usage.input_video_seconds, total_seconds: usage.seconds + usage.input_video_seconds };
      result.content = { url: (ctx.serverAddress || "").replace(/\/$/, "") + "/v1/videos/" + encodeURIComponent(task.task_id) + "/content" };
    } else if (task.status === "FAILURE") {
      if (data.status === "cancelled") result.status = "cancelled";
      result.error = { message: task.fail_reason || "Video generation failed" };
    }
    return { task: result };
  },
  error: function (_ctx, error) {
    return { type: "error", error: { type: error.code, message: error.message, http_code: String(error.httpStatus) }, request_id: error.requestId };
  },
};

export function buildSubmitRequest(ctx) {
  const request = videoRequest(ctx.requestBody, ctx.upstreamModel || ctx.model);
  if (!ctx.publicTaskId) throw new Error("public task ID is required for the submission order key");
  return {
    url: ctx.baseUrl + "/v1/videos",
    method: "POST",
    headers: {
      Authorization: "Bearer " + ctx.apiKey,
      "Content-Type": "application/json",
      "Idempotency-Key": ctx.publicTaskId,
    },
    body: request,
    action: ACTIONS[request.task],
  };
}

export function parseSubmitResponse(ctx, response) {
  const body = response.body;
  if (!isObject(body) || typeof body.id !== "string" || !body.id.trim()) throw new Error("Jiliu submission did not return a task id");
  const conditions = (ctx.requestBody && ctx.requestBody.conditions) || [];
  return {
    taskId: body.id,
    taskData: body,
    state: {
      inputVideoCount: conditions.filter(function (condition) {
        return condition.type === "video";
      }).length,
    },
  };
}

export function extractUsage(ctx) {
  // Resolution is a condition for expressions, not a legacy price multiplier.
  // Only duration participates in the legacy per-second pricing path.
  const request = videoRequest(ctx.requestBody, ctx.upstreamModel || ctx.model);
  if (ctx.usagePurpose === "billing_ratios") return { seconds: request.target.duration_seconds };
  const resolution = [480, 768, 1080].includes(request.target.short_edge) ? request.target.short_edge + "P" : "other";
  // Jiliu probes reference durations asynchronously. Reserve one requested
  // output duration per input video, then settle to the provider's measured
  // input_video_seconds. Client-supplied input durations are never billed.
  const inputVideos = (request.conditions || []).filter(function (condition) {
    return condition.type === "video";
  }).length;
  return {
    seconds: request.target.duration_seconds,
    resolution: resolution,
    task: request.task,
    input_images: (request.conditions || []).filter(function (condition) {
      return condition.type === "image";
    }).length,
    input_video_seconds: inputVideos * request.target.duration_seconds,
  };
}

function completedUsage(ctx, body) {
  if (!isObject(body)) throw new Error("Jiliu completion usage is missing");
  const videos = Array.isArray(body.input_videos) ? body.input_videos : [];
  const expected = ctx.state && ctx.state.inputVideoCount !== undefined ? ctx.state.inputVideoCount : videos.length;
  let inputSeconds = 0;
  if (expected > 0) {
    if (body.input_video_duration_status !== "ready" || videos.length !== expected) throw new Error("Jiliu input video duration is not ready");
    inputSeconds = body.input_video_seconds;
    if (typeof inputSeconds !== "number" || !Number.isFinite(inputSeconds) || inputSeconds <= 0 || inputSeconds > 3600) {
      throw new Error("Jiliu input video duration is invalid");
    }
  }
  const seconds = Number(body.seconds);
  if (!Number.isInteger(seconds) || seconds < 4 || seconds > 15) throw new Error("Jiliu output video duration is invalid");
  const facts = { seconds: seconds, input_video_seconds: inputSeconds };
  const resolution = body.metadata && typeof body.metadata.resolution === "string" ? body.metadata.resolution.toUpperCase() : "";
  if (resolution) facts.resolution = ["480P", "768P", "1080P"].includes(resolution) ? resolution : "other";
  return facts;
}

export function extractUsageOnComplete(ctx, result, body) {
  return result && result.status === "SUCCESS" ? completedUsage(ctx, body) : {};
}

export function buildQueryRequest(ctx) {
  return {
    url: ctx.baseUrl + "/v1/videos/" + encodeURIComponent(ctx.taskId),
    method: "GET",
    headers: { Authorization: "Bearer " + ctx.apiKey },
  };
}

export function parseTaskResult(ctx, body) {
  const statuses = {
    queued: "QUEUED",
    running: "IN_PROGRESS",
    in_progress: "IN_PROGRESS",
    completed: "SUCCESS",
    failed: "FAILURE",
    expired: "FAILURE",
    cancelled: "FAILURE",
  };
  const status = isObject(body) ? body.status : undefined;
  const mapped = Object.prototype.hasOwnProperty.call(statuses, status) ? statuses[status] : "UNKNOWN";
  // Do not publish success and settle an estimate when provider metering is
  // still pending or malformed; the poller can retry the snapshot instead.
  if (mapped === "SUCCESS") completedUsage(ctx, body);
  const result = { status: mapped };
  if (mapped === "UNKNOWN") result.reason = "Unrecognized Jiliu task status";
  if (mapped === "FAILURE") {
    result.reason =
      typeof body.error === "string" ? body.error : body.error && typeof body.error.message === "string" ? body.error.message : "Jiliu task " + status;
  }
  return result;
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  return {
    url: ctx.baseUrl + "/v1/videos/" + encodeURIComponent(ctx.upstreamTaskId) + "/content",
    // The provider documents GET only. The host omits the body for HEAD clients.
    method: "GET",
    headers: { Authorization: "Bearer " + ctx.apiKey },
    allowCredentiallessRedirects: true,
  };
}

export const protocols = {
  minimax_video: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json" || !isObject(ctx.body.value)) throw new Error("Jiliu H3 requires a JSON request body");
      const request = officialRequest(Object.assign({}, ctx.body.value, { model: ctx.upstreamModel || ctx.model }));
      return { kind: "submit", model: ctx.model, action: ACTIONS[request.task], requestBody: request };
    },
    render: function (ctx, task) {
      return native.taskStatus(ctx, task);
    },
  },
  openai_video: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("Jiliu H3 requires a JSON request body");
      const request = videoRequest(ctx.body.value, ctx.model);
      return {
        kind: "submit",
        model: ctx.model,
        action: ACTIONS[request.task],
        requestBody: request,
      };
    },
    render: function (ctx, task) {
      const result = isObject(task.data) ? Object.assign({}, task.data) : {};
      if (isObject(result.metadata) && result.metadata.url) {
        // Jiliu currently returns an upstream localhost URL here. Downloads
        // must use this gateway's authenticated public task content route.
        result.metadata = Object.assign({}, result.metadata, { url: "/v1/videos/" + encodeURIComponent(task.task_id) + "/content" });
      }
      return result;
    },
  },
};
