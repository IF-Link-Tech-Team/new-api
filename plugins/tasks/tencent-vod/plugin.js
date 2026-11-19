// VOD WAND-Vega: model/version and media limits follow the provider's
// onboarding guide. Prices are administrator-owned provider expressions.
const VIDEO_MODELS = {
  "wand-vega-video-1.0-lite": {
    version: "1.0-lite",
    min: 4,
    max: 15,
    images: 9,
    videos: 3,
    audios: 3,
    files: 12,
    inputSeconds: 15,
    resolutions: ["480P", "768P", "1080P", "2K", "4K"],
  },
  "wand-vega-video-1.5-lite": {
    version: "1.5-lite",
    min: 2,
    max: 30,
    auto: true,
    images: 10,
    videos: 5,
    audios: 5,
    files: 20,
    inputSeconds: 15,
    resolutions: ["480P", "720P", "1080P", "2K", "4K"],
  },
  "wand-vega-video-1.0-pro": { version: "1.0-pro", min: 4, max: 15, auto: true, images: 9, videos: 3, audios: 3, files: 12, inputSeconds: 15 },
  "wand-vega-video-1.0-pro-fast": { version: "1.0-pro-fast", min: 4, max: 15, auto: true, images: 9, videos: 3, audios: 3, files: 12, inputSeconds: 15 },
  "wand-vega-video-1.0-pro-mini": { version: "1.0-pro-mini", min: 4, max: 15, auto: true, images: 9, videos: 3, audios: 3, files: 12, inputSeconds: 15 },
  "wand-vega-video-1.5-pro": { version: "1.5-pro", min: 4, max: 30, auto: true, images: 30, videos: 10, audios: 10, files: 50, inputSeconds: 300 },
};
// Declare shared public names without changing the provider's model versions.
VIDEO_MODELS["MiniMax-H3"] = VIDEO_MODELS["wand-vega-video-1.0-lite"];
VIDEO_MODELS["wan-3.0"] = VIDEO_MODELS["wand-vega-video-1.5-lite"];
const IMAGE_MODELS = {
  "wand-vega-image-1.0-lite": { version: "1.0-lite", images: 3, max: 1 },
  "wand-vega-image-1.0-flash": { version: "1.0-flash", images: 6, max: 128 },
  "wand-vega-image-1.0-pro": { version: "1.0-pro", images: 6, max: 128 },
  "wand-vega-image-1.5-pro": { version: "1.5-pro", images: 6, max: 1 },
};
const VIDEO_RESOLUTIONS = ["480P", "720P", "768P", "1080P", "2K", "4K"];
const VIDEO_USAGE = {
  seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
  input_video_seconds: { type: "number", unit: "second", description: { en: "Input video unit price", zh: "输入视频单价" } },
  input_images: { type: "number", unit: "count", unitLabel: { en: "image", zh: "张" }, description: { en: "Input image unit price", zh: "输入图片单价" } },
  resolution: { enum: VIDEO_RESOLUTIONS, description: { en: "Output video resolution", zh: "输出视频分辨率" } },
  has_video: { type: "boolean", description: { en: "Reference video present", zh: "存在参考视频" } },
  draft: { type: "boolean", description: { en: "Draft mode enabled", zh: "启用样片模式" } },
};
const IMAGE_USAGE = {
  image_count: { type: "number", unit: "count", unitLabel: { en: "image", zh: "张" }, description: { en: "Image generation unit price", zh: "图片生成单价" } },
  input_images: VIDEO_USAGE.input_images,
  resolution: { enum: ["1K", "2K", "4K"], description: { en: "Output image resolution", zh: "输出图片分辨率" } },
};
export const meta = {
  apiVersion: 1,
  requiredCapabilities: ["duration-auto@1"],
  key: "tencent-vod",
  name: "Video Generation",
  icon: "text:Video",
  description: {
    en: "Video and image generation",
    zh: "视频与图片生成",
  },
  version: "1.2.1",
  author: { name: "IF.Link" },
  auth: "api_key",
  baseUrl: "https://vod.tencentcloudapi.com",
  models: Object.keys(VIDEO_MODELS).concat(Object.keys(IMAGE_MODELS)),
  fetchMode: "per_task",
  upstreams: ["vendor", "new_api"],
  usageSchema: VIDEO_USAGE,
  usageProfiles: [
    {
      models: Object.keys(VIDEO_MODELS),
      schema: VIDEO_USAGE,
      examples: [{ label: "720P · 5s", facts: { seconds: 5, input_video_seconds: 0, input_images: 0, resolution: "720P", has_video: false, draft: false } }],
    },
    {
      models: Object.keys(IMAGE_MODELS),
      schema: IMAGE_USAGE,
      examples: [{ label: "2K · 1 image", facts: { image_count: 1, input_images: 0, resolution: "2K" } }],
    },
  ],
  protocols: [
    { name: "minimax_video", models: Object.keys(VIDEO_MODELS) },
    { name: "openai_video", models: Object.keys(VIDEO_MODELS) },
    { name: "openai_image", models: Object.keys(IMAGE_MODELS) },
  ],
  routes: [
    { method: "POST", path: "/vod/v1/videos", type: "submit", decode: "createVideo", render: "taskCreated" },
    { method: "POST", path: "/vod/v1/images", type: "submit", decode: "createImage", render: "taskCreated" },
    { method: "GET", path: "/vod/v1/tasks/:task_id", type: "query", render: "taskStatus" },
    {
      method: "POST",
      path: "/vod/v2/video_generation",
      type: "submit",
      decode: "createMiniMaxVideo",
      render: "miniMaxCreated",
      models: Object.keys(VIDEO_MODELS),
    },
    { method: "GET", path: "/vod/v2/query/video_generation/:task_id", type: "query", render: "miniMaxStatus" },
  ],
};

function object(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
function text(value) {
  return typeof value === "string" ? value.trim() : "";
}
function modelInfo(model) {
  const info = Object.prototype.hasOwnProperty.call(VIDEO_MODELS, model)
    ? VIDEO_MODELS[model]
    : Object.prototype.hasOwnProperty.call(IMAGE_MODELS, model)
      ? IMAGE_MODELS[model]
      : null;
  if (!info) throw new Error("unsupported WAND-Vega model");
  return info;
}
function imageModel(model) {
  return Object.prototype.hasOwnProperty.call(IMAGE_MODELS, model);
}
function number(value, min, max, name) {
  if (typeof value !== "number" || !Number.isFinite(value) || value < min || value > max) throw new Error(name + " is out of range");
  return value;
}
function extra(body) {
  if (body.ExtInfo === undefined || body.ExtInfo === "") return {};
  if (typeof body.ExtInfo !== "string") throw new Error("ExtInfo must be a JSON string");
  const result = JSON.parse(body.ExtInfo);
  if (!object(result)) throw new Error("ExtInfo must contain an object");
  for (const field of ["WandVegaParameters", "AdditionalParameters"]) {
    if (result[field] === undefined) result[field] = {};
    else if (typeof result[field] === "string") result[field] = JSON.parse(result[field]);
    if (!object(result[field])) throw new Error(field + " must contain an object");
  }
  return result;
}
function draftId(body) {
  return text((extra(body).AdditionalParameters || {}).draft_task_id);
}

function normalize(body, model) {
  if (!object(body)) throw new Error("JSON object required");
  const info = modelInfo(model);
  const isImage = imageModel(model);
  const result = utils.json.clone(body);
  delete result.model;
  result.ModelName = isImage ? "wand-vega-image" : "wand-vega-video";
  result.ModelVersion = info.version;
  if (result.OutputConfig !== undefined && !object(result.OutputConfig)) throw new Error("OutputConfig must be an object");
  result.OutputConfig = Object.assign({ StorageMode: "Temporary" }, result.OutputConfig || {});
  const output = result.OutputConfig;
  if (output.StorageMode !== "Temporary" && output.StorageMode !== "Permanent") throw new Error("unsupported StorageMode");
  const refs = result.FileInfos === undefined ? [] : result.FileInfos;
  if (!Array.isArray(refs)) throw new Error("FileInfos must be an array");
  const counts = { Image: 0, Video: 0, Audio: 0 };
  const frames = new Set();
  for (const file of refs) {
    if (!object(file)) throw new Error("FileInfos entries must be objects");
    if (isImage) file.Category = "Image";
    if (!Object.prototype.hasOwnProperty.call(counts, file.Category)) throw new Error("unsupported reference Category");
    counts[file.Category]++;
    if (file.Type === "File") {
      if (!text(file.FileId)) throw new Error("FileId is required");
    } else if (file.Type === "Url") {
      if (!/^https?:\/\/[^\s/?#]+(?:[^\s]*)$/.test(text(file.Url)) && !/^asset:\/\/asset-[\w-]+$/.test(text(file.Url)))
        throw new Error("reference Url must be HTTP, HTTPS or a VOD asset");
    } else if (isImage && file.Type === "Base64") {
      if (!text(file.Base64)) throw new Error("Base64 is required");
    } else throw new Error("unsupported reference Type");
    if (!isImage) {
      file.Usage = file.Usage || "Reference";
      if (!["Reference", "FirstFrame", "LastFrame"].includes(file.Usage)) throw new Error("unsupported reference Usage");
      if (file.Usage !== "Reference") {
        if (file.Category !== "Image" || frames.has(file.Usage)) throw new Error("keyframes must be unique images");
        frames.add(file.Usage);
      }
    }
  }
  if (counts.Image > info.images || (!isImage && (counts.Video > info.videos || counts.Audio > info.audios || refs.length > info.files)))
    throw new Error("reference media limit exceeded for this model");
  if (counts.Audio && !counts.Image && !counts.Video && info.version !== "1.5-lite") throw new Error("audio references require an image or video");
  if (frames.size && refs.length !== frames.size) throw new Error("keyframes and references cannot be mixed");
  if (frames.has("LastFrame") && !frames.has("FirstFrame")) throw new Error("a last frame requires a first frame");
  if (result.Prompt !== undefined && typeof result.Prompt !== "string") throw new Error("Prompt must be a string");
  if (info.version === "1.0-lite" && text(result.Prompt).length > 7000) throw new Error("Prompt must not exceed 7000 characters");
  const draft = draftId(result);
  if (draft && (model !== "wand-vega-video-1.5-pro" || result.Prompt !== undefined || refs.length))
    throw new Error("draft upgrade requires 1.5-pro and no Prompt or FileInfos");
  if (!draft && !text(result.Prompt) && !refs.length) throw new Error("Prompt or reference media is required");
  if (output.AspectRatio !== undefined && !["21:9", "16:9", "4:3", "1:1", "3:4", "9:16", "adaptive"].includes(output.AspectRatio))
    throw new Error("unsupported AspectRatio");
  if (isImage) {
    output.Resolution = String(output.Resolution || "1K").toUpperCase();
    if (output.Resolution === "1080P") output.Resolution = "1K";
    if (!["1K", "2K", "4K"].includes(output.Resolution)) throw new Error("unsupported image Resolution");
    const count = output.OutputImageCount === undefined ? 1 : output.OutputImageCount;
    number(count, 1, info.max, "OutputImageCount");
    if (!Number.isInteger(count)) throw new Error("OutputImageCount must be an integer");
    output.OutputImageCount = count;
  } else {
    output.Resolution = String(output.Resolution || (info.version === "1.0-lite" ? "768P" : "720P")).toUpperCase();
    if (!(info.resolutions || ["480P", "720P", "1080P", "2K", "4K"]).includes(output.Resolution)) throw new Error("unsupported video Resolution");
    const duration = output.Duration === undefined ? 5 : output.Duration;
    if (!(duration === -1 && info.auto)) number(duration, info.min, info.max, "Duration");
    output.Duration = duration;
    if (output.AudioGeneration !== undefined && !["Enabled", "Disabled"].includes(output.AudioGeneration))
      throw new Error("AudioGeneration must be Enabled or Disabled");
    const additional = extra(result).AdditionalParameters || {};
    if (additional.draft !== undefined && typeof additional.draft !== "boolean") throw new Error("draft must be a boolean");
    if (additional.draft && (model !== "wand-vega-video-1.5-pro" || output.Resolution !== "480P" || draft))
      throw new Error("draft mode requires 1.5-pro at 480P");
    if (draft) {
      if (output.Resolution !== "1080P") throw new Error("draft upgrade requires 1080P");
      delete output.Duration;
    }
  }
  return result;
}

function nativeIntent(ctx, isImage) {
  if (!ctx.body || ctx.body.kind !== "json") throw new Error("VOD requires a JSON request body");
  const body = ctx.body.value;
  if (!object(body)) throw new Error("JSON object required");
  const model = text(body.model) || text(body.ModelName) + "-" + text(body.ModelVersion);
  if (imageModel(model) !== isImage) throw new Error("model does not match this endpoint");
  const normalized = normalize(body, model);
  const intent = { kind: "submit", model: model, action: isImage ? "image_generation" : "video_generation", requestBody: normalized };
  const origin = draftId(normalized);
  if (origin) intent.originTaskIds = [origin];
  return intent;
}

// MiniMax V2 is an input/output surface only. The driver, persisted task and
// measured billing still use the same VOD request as the other protocols.
function miniMaxVideoIntent(ctx) {
  if (!ctx.body || ctx.body.kind !== "json" || !object(ctx.body.value)) throw new Error("MiniMax video generation requires a JSON object");
  const value = ctx.body.value;
  const model = ctx.upstreamModel || ctx.model || value.model;
  const allowed = ["model", "content", "resolution", "duration", "ratio", "callback_url", "aigc_watermark"];
  for (const key of Object.keys(value)) if (!allowed.includes(key)) throw new Error("VOD MiniMax compatibility does not support the field: " + key);
  if (!Object.prototype.hasOwnProperty.call(VIDEO_MODELS, model)) throw new Error("model must be a WAND-Vega video model");
  if (value.callback_url !== undefined && value.callback_url !== "")
    throw new Error("VOD MiniMax compatibility does not support callbacks; poll the task instead");
  if (value.aigc_watermark !== undefined) throw new Error("VOD MiniMax compatibility does not support aigc_watermark");
  if (!Number.isInteger(value.duration) || value.duration <= 0) throw new Error("duration must be a positive integer");
  if (typeof value.resolution !== "string" || !value.resolution) throw new Error("resolution is required");
  if (!Array.isArray(value.content) || !value.content.length) throw new Error("content must be a non-empty array");
  const prompts = [];
  const files = [];
  let hasFrames = false;
  for (const item of value.content) {
    if (!object(item)) throw new Error("content entries must be objects");
    if (item.type === "text") {
      if (
        Object.keys(item).some(function (key) {
          return key !== "type" && key !== "text";
        }) ||
        !text(item.text)
      )
        throw new Error("text content must contain non-empty text only");
      prompts.push(item.text);
      continue;
    }
    const category = { image_url: "Image", video_url: "Video", audio_url: "Audio" }[item.type];
    if (
      !category ||
      Object.keys(item).some(function (key) {
        return key !== "type" && key !== "role" && key !== item.type;
      })
    )
      throw new Error("unsupported media content field");
    const media = item[item.type];
    if (
      !object(media) ||
      Object.keys(media).some(function (key) {
        return key !== "url";
      }) ||
      !text(media.url)
    )
      throw new Error("media content must contain a URL only");
    const role = item.role === undefined ? { Image: "first_frame", Video: "reference_video", Audio: "reference_audio" }[category] : item.role;
    let usage = "Reference";
    if (category === "Image" && (role === "first_frame" || role === "last_frame")) {
      hasFrames = true;
      usage = role === "first_frame" ? "FirstFrame" : "LastFrame";
    } else if (role !== { Image: "reference_image", Video: "reference_video", Audio: "reference_audio" }[category])
      throw new Error("unsupported role for " + item.type);
    files.push({ Type: "Url", Url: media.url, Category: category, Usage: usage });
  }
  if (!prompts.length) throw new Error("content requires a non-empty text item");
  const ratio = value.ratio === undefined ? "adaptive" : value.ratio;
  if (!["adaptive", "21:9", "16:9", "4:3", "1:1", "3:4", "9:16"].includes(ratio)) throw new Error("unsupported ratio");
  if (!files.length && ratio === "adaptive") throw new Error("text-to-video requires an explicit ratio");
  const intent = nativeIntent(
    {
      body: {
        kind: "json",
        value: {
          model: model,
          Prompt: prompts.join("\n"),
          FileInfos: files,
          OutputConfig: { Duration: value.duration, Resolution: value.resolution, AspectRatio: hasFrames ? "adaptive" : ratio },
        },
      },
    },
    false
  );
  intent.model = ctx.model || value.model;
  return intent;
}

function compatibleIntent(ctx, isImage) {
  if (!ctx.body || ctx.body.kind !== "json") throw new Error("VOD requires JSON; use URL or Base64 image references");
  const req = ctx.body.value;
  if (!object(req)) throw new Error("JSON object required");
  if (req.metadata !== undefined && !object(req.metadata)) throw new Error("metadata must be an object");
  const clientModel = ctx.model || text(req.model);
  const model = ctx.upstreamModel || clientModel;
  if (imageModel(model) !== isImage) throw new Error("model does not match this endpoint");
  const body = Object.assign({}, object(req.metadata) ? req.metadata : {});
  if (req.prompt !== undefined) body.Prompt = req.prompt;
  body.OutputConfig = Object.assign({}, body.OutputConfig || {});
  const output = body.OutputConfig;
  if (req.resolution !== undefined) output.Resolution = req.resolution;
  if (req.aspect_ratio !== undefined) output.AspectRatio = req.aspect_ratio;
  if (req.seconds !== undefined) output.Duration = Number(req.seconds);
  if (req.duration !== undefined) output.Duration = req.duration;
  if (req.generate_audio !== undefined) {
    if (typeof req.generate_audio !== "boolean") throw new Error("generate_audio must be boolean");
    output.AudioGeneration = req.generate_audio ? "Enabled" : "Disabled";
  }
  if (req.size !== undefined) {
    const dimensions = {
      "1024x1024": ["1K", "1:1"],
      "1280x720": ["720P", "16:9"],
      "720x1280": ["720P", "9:16"],
      "1920x1080": ["1080P", "16:9"],
      "1080x1920": ["1080P", "9:16"],
    };
    if (dimensions[req.size]) {
      output.Resolution = dimensions[req.size][0];
      output.AspectRatio = dimensions[req.size][1];
    } else if (typeof req.size === "string" && /^(480P|720P|768P|1080P|1K|2K|4K)$/i.test(req.size)) output.Resolution = req.size;
    else throw new Error("unsupported size; use resolution and aspect_ratio");
  }
  if (isImage) {
    if (req.n !== undefined) output.OutputImageCount = req.n;
    if (req.response_format !== undefined && !["url", "b64_json"].includes(req.response_format)) throw new Error("unsupported response_format");
  }
  const images = req.image === undefined ? req.images || [] : Array.isArray(req.image) ? req.image : [req.image];
  if (!Array.isArray(images)) throw new Error("images must be an array");
  const files = utils.json.clone(body.FileInfos || []);
  for (const image of images) {
    if (typeof image !== "string") throw new Error("image references must be URL or Base64 strings");
    const data = /^data:image\/[\w.+-]+;base64,(.+)$/.exec(image);
    if (data && isImage) files.push({ Type: "Base64", Base64: data[1], Category: "Image" });
    else files.push({ Type: "Url", Url: image, Category: "Image", Usage: "Reference" });
  }
  if (req.content !== undefined) {
    if (!Array.isArray(req.content)) throw new Error("content must be an array");
    const prompts = [];
    for (const part of req.content) {
      if (!object(part)) throw new Error("content entries must be objects");
      if (part.type === "text") {
        prompts.push(text(part.text));
        continue;
      }
      const category = { image_url: "Image", video_url: "Video", audio_url: "Audio" }[part.type];
      if (!category || !object(part[part.type])) throw new Error("unsupported content type");
      const usage = {
        first_frame: "FirstFrame",
        last_frame: "LastFrame",
        reference_image: "Reference",
        reference_video: "Reference",
        reference_audio: "Reference",
      }[part.role || "reference_image"];
      if (!usage) throw new Error("unsupported media role");
      files.push({ Type: "Url", Url: part[part.type].url, Category: category, Usage: usage });
    }
    if (prompts.length) body.Prompt = prompts.join("\n");
  }
  if (files.length) body.FileInfos = files;
  const intent = nativeIntent({ body: { kind: "json", value: Object.assign(body, { model: model }) } }, isImage);
  intent.model = clientModel;
  return intent;
}

function gateway(ctx) {
  return ctx.upstream && ctx.upstream.kind === "new_api";
}
function credentials(ctx) {
  const parts = String(ctx.apiKey || "")
    .split("|")
    .map(text);
  if ((parts.length !== 3 && parts.length !== 4) || !/^[1-9]\d*$/.test(parts[0]) || !parts[1] || !parts[2])
    throw new Error("VOD channel key must be SubAppId|SecretId|SecretKey, optionally followed by |SessionToken");
  const id = Number(parts[0]);
  if (!Number.isSafeInteger(id)) throw new Error("invalid VOD SubAppId");
  return { subAppId: id, secretId: parts[1], secretKey: parts[2], token: parts[3] || "" };
}
function request(ctx, action, body) {
  const auth = credentials(ctx);
  body.SubAppId = auth.subAppId;
  const serialized = JSON.stringify(body);
  return {
    url: ctx.baseUrl.replace(/\/+$/, "") + "/",
    method: "POST",
    body: serialized,
    headers: utils.tencentSignTC3({
      url: ctx.baseUrl,
      body: serialized,
      secretId: auth.secretId,
      secretKey: auth.secretKey,
      token: auth.token,
      action: action,
      version: "2018-07-17",
    }),
  };
}
function resolvedRequest(ctx) {
  const model = ctx.upstreamModel || ctx.model;
  const body = normalize(ctx.requestBody, model);
  const origin = draftId(body);
  if (origin) {
    const task = (ctx.originTasks || []).find(function (candidate) {
      return candidate.taskId === origin;
    });
    if (!task || task.status !== "SUCCESS") throw new Error("draft upgrade requires a completed owned draft task");
    const prior = taskNode(task.data, false);
    if (!prior || !(extra(prior.Input || {}).AdditionalParameters || {}).draft) throw new Error("origin task is not a VOD draft");
    const ext = extra(body);
    ext.AdditionalParameters.draft_task_id = task.upstreamTaskId;
    body.ExtInfo = JSON.stringify(
      Object.assign({}, ext, {
        AdditionalParameters: JSON.stringify(ext.AdditionalParameters),
        WandVegaParameters: JSON.stringify(ext.WandVegaParameters || {}),
      })
    );
  }
  return body;
}

export function buildSubmitRequest(ctx) {
  const isImage = imageModel(ctx.upstreamModel || ctx.model);
  if (gateway(ctx))
    return {
      url: ctx.baseUrl.replace(/\/+$/, "") + (isImage ? "/vod/v1/images" : "/vod/v1/videos"),
      method: "POST",
      headers: { Authorization: "Bearer " + ctx.apiKey, "Content-Type": "application/json" },
      body: Object.assign(resolvedRequest(ctx), { model: ctx.upstreamModel || ctx.model }),
    };
  const body = resolvedRequest(ctx);
  if (isImage && body.FileInfos) {
    for (const file of body.FileInfos) {
      delete file.Category;
      delete file.Usage;
    }
  }
  if (!isImage) {
    const ext = extra(body);
    ext.WandVegaParameters = Object.assign({}, ext.WandVegaParameters || {}, { ReturnVegaUsage: true });
    if (ext.AdditionalParameters) ext.AdditionalParameters = JSON.stringify(ext.AdditionalParameters);
    ext.WandVegaParameters = JSON.stringify(ext.WandVegaParameters);
    body.ExtInfo = JSON.stringify(ext);
  }
  body.SessionId = ctx.publicTaskId;
  return request(ctx, isImage ? "CreateAigcImageTask" : "CreateAigcVideoTask", body);
}
function response(body) {
  if (!object(body) || !object(body.Response)) throw new Error("VOD response is missing Response");
  if (body.Response.Error) throw new Error(String(body.Response.Error.Code || "VOD error") + ": " + String(body.Response.Error.Message || "request failed"));
  return body.Response;
}
export function parseSubmitResponse(ctx, resp) {
  const data = response(resp.body);
  if (!text(data.TaskId)) throw new Error("VOD response is missing TaskId");
  const facts = extractUsage(Object.assign({}, ctx, { usagePurpose: "facts" }));
  return {
    taskId: data.TaskId,
    taskData: resp.body,
    state: { inputImages: facts.input_images, hasVideo: facts.has_video || false, resolution: facts.resolution, draft: facts.draft || false },
  };
}
export function buildQueryRequest(ctx) {
  if (gateway(ctx))
    return {
      url: ctx.baseUrl.replace(/\/+$/, "") + "/vod/v1/tasks/" + encodeURIComponent(ctx.taskId),
      method: "GET",
      headers: { Authorization: "Bearer " + ctx.apiKey },
    };
  return request(ctx, "DescribeTaskDetail", { TaskId: ctx.taskId });
}
function taskNode(body, isImage) {
  const data = response(body);
  return data[isImage ? "AigcImageTask" : "AigcVideoTask"];
}
function artifacts(body, isImage) {
  const node = taskNode(body, isImage);
  const files = node && node.Output && node.Output.FileInfos;
  const seen = new Set();
  return (Array.isArray(files) ? files : []).filter(function (f) {
    if (!object(f) || !/^https?:\/\//.test(text(f.FileUrl)) || seen.has(f.FileUrl)) return false;
    seen.add(f.FileUrl);
    return true;
  });
}
function completionUsage(ctx, body) {
  const model = ctx.upstreamModel || ctx.model;
  const isImage = imageModel(model);
  const node = taskNode(body, isImage);
  if (!node || !object(node.Output)) throw new Error("VOD task output is missing");
  const info = modelInfo(model);
  const state = ctx.state || {};
  const input = node.Input || {};
  const inputFiles = Array.isArray(input.FileInfos) ? input.FileInfos : [];
  const inputImages =
    state.inputImages === undefined
      ? inputFiles.filter(function (f) {
          return f.Category === "Image" || isImage;
        }).length
      : state.inputImages;
  const hasVideo =
    state.hasVideo === undefined
      ? inputFiles.some(function (f) {
          return f.Category === "Video";
        })
      : state.hasVideo;
  const outputConfig = input.OutputConfig || {};
  const resolution = String(outputConfig.Resolution || state.resolution || "").toUpperCase();
  if (isImage) {
    const urls = new Set(
      artifacts(body, true).map(function (file) {
        return file.FileUrl;
      })
    );
    number(urls.size, 1, 128, "output image count");
    return { image_count: urls.size, input_images: inputImages, resolution: resolution === "1080P" ? "1K" : resolution };
  }
  const usage = node.Output.Usage;
  if (!object(usage)) throw new Error("VOD measured usage is missing");
  const seconds = number(usage.OutputSeconds, 0.001, info.max, "OutputSeconds");
  // InputSeconds is upstream metering, never client-supplied media metadata.
  let inputSeconds = hasVideo ? number(usage.InputSeconds, 0.001, info.inputSeconds, "InputSeconds") : 0;
  // Pro reference input is billed for at least ceil(output duration * 2/3).
  // Applying the lower bound is idempotent if VOD already reports billed usage.
  if (hasVideo && info.version.includes("pro")) inputSeconds = Math.max(inputSeconds, Math.ceil((seconds * 2) / 3));
  if (!(info.resolutions || VIDEO_RESOLUTIONS).includes(resolution)) throw new Error("VOD output resolution is missing or invalid");
  const billedImages = model === "wand-vega-video-1.0-lite" ? number(usage.InputImageCount, 0, info.images, "InputImageCount") : inputImages;
  if (!Number.isInteger(billedImages)) throw new Error("InputImageCount must be an integer");
  return {
    seconds: seconds,
    input_video_seconds: inputSeconds,
    input_images: billedImages,
    resolution: resolution,
    has_video: hasVideo,
    draft: state.draft === undefined ? !!(extra(input).AdditionalParameters || {}).draft : state.draft,
  };
}
export function parseTaskResult(ctx, body) {
  const node = taskNode(body, imageModel(ctx.upstreamModel || ctx.model));
  if (!object(node)) return { status: "UNKNOWN", reason: "VOD task detail is missing" };
  if (node.Status === "PROCESSING" || node.Status === "WAITING")
    return { status: node.Status === "WAITING" ? "QUEUED" : "IN_PROGRESS", progress: String(Math.max(0, Math.min(99, Number(node.Progress) || 0))) + "%" };
  if (node.Status !== "FINISH") return { status: "UNKNOWN", reason: "unrecognized VOD task status" };
  if (Number(node.ErrCode) !== 0 || text(node.ErrCodeExt))
    return { status: "FAILURE", progress: "100%", reason: text(node.ErrCodeExt) + ": " + text(node.Message) };
  completionUsage(ctx, body);
  const files = artifacts(body, imageModel(ctx.upstreamModel || ctx.model));
  const primary = imageModel(ctx.upstreamModel || ctx.model)
    ? files[0]
    : files.find(function (file) {
        return !file.UsageType;
      });
  if (!primary) throw new Error("VOD task completed without a downloadable result");
  return { status: "SUCCESS", progress: "100%", url: primary.FileUrl };
}
export function extractUsage(ctx) {
  if (ctx.usagePurpose === "billing_ratios") return null;
  const model = ctx.upstreamModel || ctx.model;
  const body = resolvedRequest(ctx);
  const images = (body.FileInfos || []).filter(function (f) {
    return f.Category === "Image";
  }).length;
  if (imageModel(model)) return { image_count: body.OutputConfig.OutputImageCount, input_images: images, resolution: body.OutputConfig.Resolution };
  const info = modelInfo(model);
  const hasVideo = (body.FileInfos || []).some(function (f) {
    return f.Category === "Video";
  });
  const origin = draftId(ctx.requestBody);
  if (origin) {
    const task = (ctx.originTasks || []).find(function (candidate) {
      return candidate.taskId === origin;
    });
    const facts = completionUsage({ model: model }, task.data);
    return Object.assign({}, facts, { resolution: body.OutputConfig.Resolution, draft: false });
  }
  return {
    seconds: body.OutputConfig.Duration === -1 ? info.max : body.OutputConfig.Duration,
    input_video_seconds: hasVideo ? info.inputSeconds : 0,
    input_images: images,
    resolution: body.OutputConfig.Resolution,
    has_video: hasVideo,
    draft: !!(extra(body).AdditionalParameters || {}).draft,
  };
}
export function extractUsageOnComplete(ctx, result, body) {
  return result && result.status === "SUCCESS" ? completionUsage(ctx, body) : {};
}
export function listArtifacts(task) {
  if (task.status !== "SUCCESS") return [];
  const isImage = !!response(task.data).AigcImageTask;
  const files = artifacts(task.data, isImage);
  if (isImage)
    return files.map(function (file, i) {
      return { key: "image_" + i, type: "image" };
    });
  const result = [];
  if (
    files.some(function (file) {
      return !file.UsageType;
    })
  )
    result.push({ key: "video", type: "video" });
  if (
    files.some(function (file) {
      return file.UsageType === "last_frame_url";
    })
  )
    result.push({ key: "last_frame", type: "image" });
  return result;
}
export function buildContentRequest(ctx) {
  const isImage = !!response(ctx.data).AigcImageTask;
  const files = artifacts(ctx.data, isImage);
  let file;
  if (ctx.artifactKey === "last_frame")
    file = files.find(function (f) {
      return f.UsageType === "last_frame_url";
    });
  else if (isImage && /^image_\d+$/.test(ctx.artifactKey)) file = files[Number(ctx.artifactKey.slice(6))];
  else if (!isImage && ctx.artifactKey === "video")
    file = files.find(function (f) {
      return !f.UsageType;
    });
  if (!file) throw new Error("artifact_not_found");
  return { url: file.FileUrl, method: ctx.clientRequest.method, credentialless: true };
}
export const native = {
  createMiniMaxVideo: miniMaxVideoIntent,
  miniMaxCreated: function (_ctx, task) {
    return { task_id: task.task_id };
  },
  miniMaxStatus: function (ctx, task) {
    const properties = task.properties || {};
    const model = ctx.model || properties.origin_model_name;
    const statuses = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "running", SUCCESS: "succeeded", FAILURE: "failed" };
    const result = {
      id: task.task_id,
      model: model,
      status: statuses[task.status] || "unknown",
      created_at: task.created_at,
      updated_at: task.updated_at || task.created_at,
      task_type: "generation",
      modality: "video",
    };
    if (task.status === "SUCCESS") {
      const usage = completionUsage({ model: ctx.upstreamModel || properties.upstream_model_name || model }, task.data);
      const primary = artifacts(task.data, false).find(function (file) {
        return !file.UsageType;
      });
      if (!primary) throw new Error("VOD task completed without a downloadable video");
      result.content = { url: primary.FileUrl };
      result.duration = usage.seconds;
      result.resolution = usage.resolution;
      result.usage = {
        input_seconds: usage.input_video_seconds,
        output_seconds: usage.seconds,
        total_seconds: usage.input_video_seconds + usage.seconds,
        input_image_count: usage.input_images,
      };
      const output = taskNode(task.data, false).Input;
      const ratio = output && output.OutputConfig && output.OutputConfig.AspectRatio;
      if (ratio && ratio !== "adaptive") result.ratio = ratio;
    } else if (task.status === "FAILURE") result.error = { message: task.fail_reason || "Video generation failed" };
    return { task: result };
  },
  error: function (ctx, error) {
    if (ctx.path.startsWith("/vod/v2/"))
      return { type: "error", error: { type: error.code, message: error.message, http_code: String(error.httpStatus) }, request_id: error.requestId };
    // Keep the existing VOD native routes' host error envelope unchanged.
    return { code: error.code, message: error.message + (error.requestId ? " (request id: " + error.requestId + ")" : ""), data: null };
  },
  createVideo: function (ctx) {
    return nativeIntent(ctx, false);
  },
  createImage: function (ctx) {
    return nativeIntent(ctx, true);
  },
  taskCreated: function (ctx, task) {
    return { Response: { TaskId: task.task_id } };
  },
  taskStatus: function (ctx, task) {
    const body = utils.json.clone(task.data || { Response: {} });
    const data = body.Response || (body.Response = {});
    delete data.TaskId;
    for (const key of ["AigcVideoTask", "AigcImageTask"]) if (object(data[key])) data[key].TaskId = task.task_id;
    data.TaskId = task.task_id;
    data.Status = task.status === "SUCCESS" || task.status === "FAILURE" ? "FINISH" : "PROCESSING";
    if (task.status === "FAILURE") data.Error = { Code: "TaskFailed", Message: task.fail_reason || "VOD task failed" };
    return body;
  },
};
export const protocols = {
  minimax_video: {
    decodeRequest: miniMaxVideoIntent,
    render: function (ctx, task) {
      return native.miniMaxStatus(ctx, task);
    },
  },
  openai_video: {
    decodeRequest: function (ctx) {
      return compatibleIntent(ctx, false);
    },
    render: function (ctx, task) {
      const status =
        { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "in_progress", SUCCESS: "completed", FAILURE: "failed" }[task.status] ||
        "queued";
      const result = { id: task.task_id, object: "video", status: status, model: (task.properties || {}).origin_model_name, created_at: task.created_at };
      if (task.status === "FAILURE") result.error = { message: task.fail_reason };
      return result;
    },
  },
  openai_image: {
    decodeRequest: function (ctx) {
      return compatibleIntent(ctx, true);
    },
    render: function (ctx, task) {
      return {
        data: artifacts(task.data, true).map(function (f) {
          return { url: f.FileUrl };
        }),
      };
    },
  },
};
