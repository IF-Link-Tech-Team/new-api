// TokenHub's media APIs are distinct from its OpenAI-compatible chat API.
const IMAGES = [
  "wand-vega-image-lite",
  "wand-vega-image-flash",
  "wand-vega-image-pro",
  "wand-vega-image-1.5-pro",
  "seedream-image-v5.0-flash",
  "seedream-image-v5.0-lite",
  "seedream-image-v5.0-pro",
  "minimax-image-h3",
  "minimax-image-h3-highspeed",
];
const VIDEOS = [
  "wand-vega-video-1.0-lite",
  "wand-vega-video-1.5-lite",
  "minimax-video-h3",
  "minimax-video-h3-max",
  "minimax-video-v2.3",
  "minimax-video-v2.3-fast",
  "kling-video-v4-flash",
  "pixverse-video-v6.0",
  "pixverse-video-c1",
  "pixverse-video-v5.6",
];
// Public identities are also declared so shared H3 and Wan routes can select
// this provider before the channel's upstream model mapping is applied.
const VIDEO_ALIASES = {
  "MiniMax-H3": "wand-vega-video-1.0-lite",
  "wan-3.0": "wand-vega-video-1.5-lite",
  "minimax-video-h3-fast": "minimax-video-h3-max",
};
const PUBLIC_VIDEOS = VIDEOS.concat(Object.keys(VIDEO_ALIASES));
const ASR = ["hy-asr-3.0-preview", "wand-asr-v1", "tenlai-asr-v1.0"];
const MINIMAX_TTS = ["minimax-speech-2.8-hd", "minimax-speech-2.8-turbo", "minimax-speech-2.6-hd", "minimax-speech-2.6-turbo", "minimax-speech-02-hd", "minimax-speech-02-turbo"];
const AUDIO = ["minimax-music-v3.0", "indextts-2", "tenlai-tts-v1.0"].concat(MINIMAX_TTS, ASR);
const HY3D_MODEL = "hy-3d-3.1";
const HY3D_USAGE = {
  model_count: {
    type: "number",
    unit: "count",
    unitLabel: { en: "model", zh: "个模型", "zh-TW": "個模型" },
    description: { en: "3D generation unit price", zh: "3D 生成单价" },
  },
  generate_type: {
    enum: ["Normal", "Geometry"],
    description: { en: "Generation type", zh: "生成类型" },
    enumLabels: { Normal: { en: "Textured model", zh: "带贴图模型" }, Geometry: { en: "Geometry only", zh: "白模" } },
  },
  pbr: { type: "number", unit: "count", unitLabel: { en: "model", zh: "个模型" }, description: { en: "PBR material unit price", zh: "PBR 材质单价" } },
  custom_face_count: {
    type: "number",
    unit: "count",
    unitLabel: { en: "model", zh: "个模型" },
    description: { en: "Custom face count unit price", zh: "自定义面数单价" },
  },
  multi_view: {
    type: "number",
    unit: "count",
    unitLabel: { en: "model", zh: "个模型" },
    description: { en: "Multi-view generation unit price", zh: "多视图生成单价" },
  },
  extra_format: {
    type: "number",
    unit: "count",
    unitLabel: { en: "model", zh: "个模型" },
    description: { en: "Additional output format unit price", zh: "附加输出格式单价" },
  },
};
const HY3D_EXAMPLES = [
  { label: "Text or image · textured", facts: { model_count: 1, generate_type: "Normal", pbr: 0, custom_face_count: 0, multi_view: 0, extra_format: 0 } },
  { label: "Text or image · geometry", facts: { model_count: 1, generate_type: "Geometry", pbr: 0, custom_face_count: 0, multi_view: 0, extra_format: 0 } },
  {
    label: "Multi-view · PBR · custom faces",
    facts: { model_count: 1, generate_type: "Normal", pbr: 1, custom_face_count: 1, multi_view: 1, extra_format: 0 },
  },
  {
    label: "Multi-view · PBR · custom faces · extra format",
    facts: { model_count: 1, generate_type: "Normal", pbr: 1, custom_face_count: 1, multi_view: 1, extra_format: 1 },
  },
];

const MODELS = IMAGES.concat(PUBLIC_VIDEOS, AUDIO, [HY3D_MODEL]);
const tokenSchema = {
  tokens: {
    type: "number",
    unit: "token",
    description: {
      en: "Media processing token unit price",
      zh: "媒体处理 Token 单价",
    },
  },
};
export const meta = {
  apiVersion: 1,
  requiredCapabilities: ["submit-once@1"],
  key: "tokenhub-media",
  name: "TokenHub Media",
  version: "1.3.2",
  author: { name: "IF.Link" },
  description: {
    en: "TokenHub images, videos, audio and HY-3D generation",
    zh: "TokenHub 图片、视频、音频与 HY-3D 生成",
  },
  baseUrl: "https://tokenhub.tencentmaas.com",
  auth: "api_key",
  models: MODELS,
  fetchMode: "per_task",
  usageSchema: tokenSchema,
  usageExamples: [{ label: "Media tokens", facts: { tokens: 10000 } }],
  usageProfiles: [{ models: [HY3D_MODEL], schema: HY3D_USAGE, examples: HY3D_EXAMPLES }],
  protocols: [
    { name: "openai_image", models: IMAGES },
    { name: "openai_video", models: PUBLIC_VIDEOS },
    { name: "minimax_video", models: PUBLIC_VIDEOS },
  ],
  routes: [
    { method: "POST", path: "/tokenhub-media/v1/3d/generations", type: "submit", models: [HY3D_MODEL], decode: "create", render: "task" },
    {
      method: "POST",
      path: "/tokenhub-media/v1/images/generations",
      type: "submit",
      models: IMAGES,
      decode: "create",
      render: "task",
    },
    {
      method: "POST",
      path: "/tokenhub-media/v1/videos",
      type: "submit",
      models: PUBLIC_VIDEOS,
      decode: "create",
      render: "task",
    },
    {
      method: "POST",
      path: "/tokenhub-media/v1/audio/generations",
      type: "submit",
      models: AUDIO,
      decode: "create",
      render: "task",
    },
    {
      method: "GET",
      path: "/tokenhub-media/v1/tasks/:task_id",
      type: "query",
      render: "task",
    },
  ],
};
function object(x) {
  return x !== null && typeof x === "object" && !Array.isArray(x);
}
function text(x) {
  return typeof x === "string" ? x.trim() : "";
}
function number(x, min, max, label) {
  if (typeof x !== "number" || !Number.isFinite(x) || x < min || x > max) throw Error(label + " is out of range");
  return x;
}
function integer(x, min, max, label) {
  number(x, min, max, label);
  if (!Number.isInteger(x)) throw Error(label + " must be an integer");
  return x;
}
function choice(x, values, label) {
  if (!values.includes(x)) throw Error("Unsupported " + label + ": " + x);
  return x;
}
function prompt(x, max) {
  if (!text(x) || Array.from(x).length > max) throw Error("prompt must contain 1–" + max + " characters");
  return x;
}
function url(x) {
  if (typeof x === "object" && x && typeof x.__fileRef === "string") return x;
  if (!/^https?:\/\/[^@/?#\s\\]+(?:[/?][^\s\\]*)?$/.test(x || "") && !/^data:(image|video|audio)\/[\w.+-]+;base64,[A-Za-z0-9+/=]+$/.test(x || ""))
    throw Error("A media URL or data URL is required");
  return x;
}
function https(x) {
  return typeof x === "string" && /^https:\/\/[^@/?#\s\\]+(?:[/?][^\s\\]*)?$/.test(x) ? x : "";
}
function fields(b, allowed) {
  if (!object(b)) throw Error("JSON object required");
  for (const k of Object.keys(b)) if (k !== "model" && !allowed.includes(k)) throw Error("Unsupported field: " + k);
}
function bools(b, names) {
  for (const k of names) if (b[k] !== undefined && typeof b[k] !== "boolean") throw Error(k + " must be boolean");
}
function modelOf(ctx) {
  const model = ctx.upstreamModel || ctx.model || (ctx.state && ctx.state.model);
  return VIDEO_ALIASES[model] || model;
}
function imageSize(size, kind) {
  if (size === undefined) return kind === "seed" ? "2K" : kind === "mini" ? "auto" : "1024x1024";
  if (kind === "seed" && ["1K", "1.5K", "2K", "3K", "4K"].includes(size)) return size;
  if (kind === "mini" && size === "auto") return size;
  const m = /^(\d+)x(\d+)$/.exec(size || "");
  if (!m) throw Error("size must be WIDTHxHEIGHT");
  const w = Number(m[1]),
    h = Number(m[2]);
  integer(w, 1, 20000, "width");
  integer(h, 1, 20000, "height");
  number(w * h, kind === "mini" ? 655360 : kind === "seed" ? 921600 : 1, kind === "seed" ? 16777216 : 20000000, "image area");
  number(w / h, kind === "mini" ? 1 / 3 : 1 / 16, kind === "mini" ? 3 : 16, "aspect ratio");
  return size;
}
function imageRequest(model, input) {
  fields(input, [
    "prompt",
    "images",
    "input",
    "n",
    "size",
    "aspect_ratio",
    "area",
    "output_format",
    "response_format",
    "output_compression",
    "aigc_watermark",
    "disable_prompt_enhanced",
    "watermark",
    "background",
    "optimize_prompt_options",
    "sequential_image_generation",
    "sequential_image_generation_options",
    "layer_decomposition",
  ]);
  const b = Object.assign({}, input, { model: model });
  const vega = model.startsWith("wand-"),
    seed = model.startsWith("seedream-");
  b.prompt = prompt(b.prompt, vega ? 8192 : seed ? 600 : 32000);
  const n = integer(b.n === undefined ? 1 : b.n, 1, seed ? 15 : vega ? 1 : 10, "n");
  bools(b, ["aigc_watermark", "disable_prompt_enhanced", "watermark", "layer_decomposition"]);
  if (b.layer_decomposition) throw Error("Layer decomposition is not available on this image generation route");
  if (b.response_format !== undefined) choice(b.response_format, ["url", "b64_json"], "response_format");
  b.size = imageSize(b.size, vega ? "vega" : seed ? "seed" : "mini");
  let refs = b.images === undefined ? [] : b.images;
  if (!Array.isArray(refs)) throw Error("images must be an array");
  refs = refs.map((x) => url(object(x) && !x.__fileRef ? x.image_url || x.url : x));
  if (vega && b.input !== undefined) {
    if (refs.length || !Array.isArray(b.input)) throw Error("Use either images or input");
    for (const item of b.input) {
      if (!object(item) || !Array.isArray(item.content)) throw Error("Invalid input");
      for (const p of item.content) {
        fields(p, ["type", "image_url"]);
        if (p.type !== "input_image") throw Error("Expected input_image");
        refs.push(url(p.image_url));
      }
    }
  }
  integer(refs.length, 0, seed && model.endsWith("lite") ? 14 : vega ? 10 : seed ? 10 : 16, "reference images");
  delete b.n;
  delete b.images;
  delete b.input;
  delete b.layer_decomposition;
  let tokens;
  if (vega) {
    fields(b, ["prompt", "size", "response_format"]);
    delete b.response_format;
    if (refs.length) b.input = [{ content: refs.map((x) => ({ type: "input_image", image_url: x })) }];
    const side = Math.max(...b.size.split("x").map(Number));
    const tier = side > 2048 ? 2 : side > 1024 ? 1 : 0;
    const rates = {
      "wand-vega-image-lite": [13500, 13500, 18000],
      "wand-vega-image-flash": [26700, 37400, 77500],
      "wand-vega-image-pro": [95000, 95000, 171000],
      "wand-vega-image-1.5-pro": [145000, 145000, 145000],
    };
    tokens = rates[model][tier] + (model.endsWith("1.5-pro") ? refs.length : model.endsWith("pro") ? Math.max(0, refs.length - 3) : 0) * 10000;
  } else if (seed) {
    fields(b, [
      "prompt",
      "size",
      "output_format",
      "response_format",
      "watermark",
      "background",
      "optimize_prompt_options",
      "sequential_image_generation",
      "sequential_image_generation_options",
    ]);
    if (refs.length) b.images = refs;
    if (model.endsWith("lite")) {
      if (["1K", "1.5K"].includes(b.size)) throw Error("Seedream Lite requires 2K, 3K or 4K");
      choice(b.sequential_image_generation || "disabled", ["auto", "disabled"], "sequential_image_generation");
      const max = b.sequential_image_generation_options && b.sequential_image_generation_options.max_images;
      const count = integer(max === undefined ? n : max, 1, 15, "max_images");
      b.sequential_image_generation = count > 1 ? "auto" : b.sequential_image_generation || "disabled";
      b.sequential_image_generation_options = { max_images: count };
      tokens = 22000 * count;
    } else {
      if (["3K", "4K"].includes(b.size)) throw Error("Seedream Pro/Flash supports up to 2K");
      if (n !== 1 || b.sequential_image_generation === "auto") throw Error("This Seedream model generates one image per request");
      delete b.sequential_image_generation;
      delete b.sequential_image_generation_options;
      tokens = (model.endsWith("flash") ? 12000 : 60000) + Math.max(0, refs.length - 1) * 2000;
    }
    b.response_format = "url";
  } else {
    fields(b, [
      "prompt",
      "size",
      "aspect_ratio",
      "area",
      "output_format",
      "response_format",
      "output_compression",
      "aigc_watermark",
      "disable_prompt_enhanced",
    ]);
    b.n = n;
    if (refs.length) b.images = refs.map((x) => ({ image_url: x }));
    if (b.area !== undefined) choice(b.area, ["1K", "2K", "4K"], "area");
    if (b.size !== "auto" && (b.area !== undefined || b.aspect_ratio !== undefined)) throw Error("Explicit size cannot be combined with area or aspect_ratio");
    if (b.aspect_ratio !== undefined && b.aspect_ratio !== "auto") {
      const r = /^(\d+):(\d+)$/.exec(b.aspect_ratio);
      if (!r) throw Error("Invalid aspect_ratio");
      number(Number(r[1]) / Number(r[2]), 1 / 3, 3, "aspect_ratio");
    }
    if (b.output_compression !== undefined) integer(b.output_compression, 0, 100, "output_compression");
    if (b.output_format !== undefined) choice(b.output_format, ["jpeg", "png", "webp"], "output_format");
    b.response_format = "url";
    // Conservative reservation, replaced by TokenHub's measured total_tokens.
    tokens = n * 100000 + refs.length * 100000;
  }
  return {
    body: b,
    tokens: tokens,
    path: vega
      ? "/v1/wand/vega-images/generations"
      : seed
        ? "/v1/wand/si-image/generation"
        : "/v1/wand/minimax-image/" + (refs.length ? "edits" : "generation"),
    sync: !vega,
  };
}
function pixverseRequest(model, input) {
  fields(input, ["content", "prompt", "first_frame_image", "last_frame_image", "duration", "resolution", "ratio", "extra"]);
  const extra = input.extra === undefined ? {} : input.extra;
  fields(extra, ["generate_audio", "generate_multi_clip", "camera_movement", "seed"]);
  bools(extra, ["generate_audio", "generate_multi_clip"]);
  const duration = integer(input.duration === undefined ? 5 : input.duration, 1, 15, "duration");
  const resolution = String(input.resolution || "720P").toUpperCase();
  choice(resolution, ["360P", "540P", "720P", "1080P"], "resolution");
  if (model === "pixverse-video-v5.6" && (![5, 8, 10].includes(duration) || (resolution === "1080P" && duration === 10)))
    throw Error("PixVerse V5.6 supports 5/8/10 seconds, or 5/8 seconds at 1080P");
  let content = input.content;
  if (content === undefined) {
    content = [{ type: "text", text: input.prompt }];
    if (input.first_frame_image) content.push({ type: "image_url", role: "first_frame", image_url: { url: input.first_frame_image } });
    if (input.last_frame_image) content.push({ type: "image_url", role: "last_frame", image_url: { url: input.last_frame_image } });
  } else if (input.prompt !== undefined || input.first_frame_image !== undefined || input.last_frame_image !== undefined) {
    throw Error("Use content or prompt/frame fields, not both");
  }
  if (!Array.isArray(content) || !content.length || content.length > 20) throw Error("content must contain 1–20 items");
  const texts = [],
    refs = [];
  let first, last;
  for (const item of content) {
    fields(item, ["type", "text", "role", "image_url", "ref_name", "reference_type"]);
    if (item.type === "text") {
      fields(item, ["type", "text"]);
      texts.push(prompt(item.text, 5000));
      continue;
    }
    if (item.type !== "image_url") throw Error("PixVerse accepts text and image inputs only");
    fields(item.image_url, ["url"]);
    const image = url(item.image_url.url);
    const role = item.role || "first_frame";
    choice(role, ["first_frame", "last_frame", "reference_image"], "image role");
    if (role === "reference_image") {
      const ref = { img_id: image, type: choice(item.reference_type || "subject", ["subject", "background"], "reference_type") };
      if (item.ref_name !== undefined) ref.ref_name = prompt(item.ref_name, 30);
      refs.push(ref);
    } else {
      if (item.ref_name !== undefined || item.reference_type !== undefined) throw Error("Reference labels require reference_image");
      if (role === "first_frame") {
        if (first) throw Error("Only one first frame is supported");
        first = image;
      } else {
        if (last) throw Error("Only one last frame is supported");
        last = image;
      }
    }
  }
  if (!texts.length) throw Error("content requires text");
  if (refs.length > 7) throw Error("PixVerse supports at most 7 reference images");
  if (refs.length && (first || last)) throw Error("Keyframes cannot be combined with reference media");
  if (last && !first) throw Error("A last frame requires a first frame");
  const body = {
    model,
    prompt: prompt(texts.join("\n"), 5000),
    duration,
    quality: resolution.toLowerCase(),
    generate_audio_switch: extra.generate_audio === true,
  };
  let mode = "text-to-video";
  if (refs.length) {
    mode = "reference-to-video";
    body.image_references = refs;
  } else if (last) {
    mode = "start-end-to-video";
    body.first_frame_img = first;
    body.last_frame_img = last;
  } else if (first) {
    mode = "image-to-video";
    body.img_id = first;
  }
  if (!first) {
    const ratios = ["16:9", "4:3", "1:1", "3:4", "9:16"];
    if (model !== "pixverse-video-v5.6") ratios.push("2:3", "3:2", "21:9");
    body.aspect_ratio = choice(input.ratio || "16:9", ratios, "ratio");
  }
  if (extra.seed !== undefined) body.seed = integer(extra.seed, 0, 2147483647, "seed");
  if (extra.generate_multi_clip === true && (model !== "pixverse-video-v6.0" || refs.length || last))
    throw Error("Multi-clip requires PixVerse V6.0 text-to-video or image-to-video");
  if (extra.generate_multi_clip !== undefined && model === "pixverse-video-v6.0" && !refs.length && !last)
    body.generate_multi_clip_switch = extra.generate_multi_clip;
  if (extra.camera_movement !== undefined) {
    if (refs.length || last) throw Error("Camera movement is only supported for text-to-video or image-to-video");
    if (typeof extra.camera_movement !== "string" || !text(extra.camera_movement)) throw Error("camera_movement must be a non-empty string");
    body.camera_movement = extra.camera_movement;
  }
  // Reserve by the published rate; settle only on measured TokenHub usage.
  const rates = {
    "pixverse-video-v6.0": { silent: [14665, 20531, 26397, 52794], audio: [20531, 26397, 35196, 67459] },
    "pixverse-video-c1": { silent: [17598, 23464, 29330, 55727], audio: [23464, 29330, 38129, 70392] },
    "pixverse-video-v5.6": { silent: [20531, 20531, 26397, 43995], audio: [46928, 46928, 52794, 87990] },
  };
  const rate = rates[model][body.generate_audio_switch ? "audio" : "silent"][["360P", "540P", "720P", "1080P"].indexOf(resolution)];
  return { body, tokens: duration * rate, duration, resolution, hasVideo: false, path: "/v1/wand/pixverse/" + mode, queryPath: "/v1/wand/pixverse/tasks/" };
}

function videoRequest(model, input) {
  if (model.startsWith("pixverse-video-")) return pixverseRequest(model, input);
  fields(input, [
    "content",
    "prompt",
    "first_frame_image",
    "last_frame_image",
    "duration",
    "resolution",
    "ratio",
    "options",
    "extra",
    "prompt_optimizer",
    "fast_pretreatment",
    "aigc_watermark",
    "callback_url",
  ]);
  const b = Object.assign({}, input, { model: model });
  const vega = model.startsWith("wand-"),
    h3 = model.startsWith("minimax-video-h3"),
    old = model.startsWith("minimax-video-v2.3"),
    kling = model === "kling-video-v4-flash";
  if (b.callback_url) throw Error("Use task polling instead of callback_url");
  delete b.callback_url;
  bools(b, ["prompt_optimizer", "fast_pretreatment", "aigc_watermark"]);
  let content = b.content;
  if (content === undefined) {
    content = [{ type: "text", text: b.prompt }];
    if (b.first_frame_image)
      content.push({
        type: "image_url",
        image_url: { url: b.first_frame_image },
        role: "first_frame",
      });
    if (b.last_frame_image)
      content.push({
        type: "image_url",
        image_url: { url: b.last_frame_image },
        role: "last_frame",
      });
  }
  if (!Array.isArray(content) || !content.length || content.length > 20) throw Error("content must contain 1–20 items");
  let texts = [],
    images = 0,
    videos = 0,
    audios = 0,
    first = 0,
    last = 0,
    refs = 0;
  content = content.map((item) => {
    fields(item, ["type", "text", "role", "image_url", "video_url", "audio_url"]);
    if (item.type === "text") {
      texts.push(prompt(item.text, old ? 2000 : kling ? 2500 : 8192));
      return { type: "text", text: item.text };
    }
    choice(item.type, ["image_url", "video_url", "audio_url"], "content type");
    const value = item[item.type];
    if (!object(value)) throw Error("Media content requires a URL object");
    fields(value, ["url"]);
    url(value.url);
    const role = item.role || (item.type === "image_url" ? "first_frame" : item.type === "video_url" ? "reference_video" : "reference_audio");
    if (item.type === "image_url") {
      choice(role, ["first_frame", "last_frame", "reference_image"], "image role");
      images++;
      if (role === "first_frame") first++;
      if (role === "last_frame") last++;
      if (role === "reference_image") refs++;
    }
    if (item.type === "video_url") {
      choice(role, ["reference_video"], "video role");
      videos++;
    }
    if (item.type === "audio_url") {
      choice(role, ["reference_audio"], "audio role");
      audios++;
    }
    return { type: item.type, [item.type]: { url: value.url }, role: role };
  });
  if (!texts.length) throw Error("content requires text");
  prompt(texts.join("\n"), old ? 2000 : kling ? 2500 : 8192);
  if (first > 1 || last > 1 || images > 10 || videos > 3 || audios > 3 || images + videos + audios > 12) throw Error("Too many reference inputs");
  if ((first || last) && (refs || videos || audios)) throw Error("Keyframes cannot be combined with reference media");
  if (last && !first) throw Error("A last frame requires a first frame");
  if (audios && !images && !videos) throw Error("Audio-only references are not supported");
  b.duration = integer(
    b.duration === undefined ? (old ? 6 : kling ? 10 : 5) : b.duration,
    old ? 6 : model.endsWith("h3-max") ? 5 : kling ? 3 : 4,
    kling ? 20 : old ? 10 : 15,
    "duration"
  );
  b.resolution = String(b.resolution || (kling ? "720P" : model.endsWith("1.5-lite") ? "1080P" : "768P")).toUpperCase();
  b.ratio = b.ratio || "16:9";
  choice(b.ratio, ["21:9", "16:9", "4:3", "1:1", "3:4", "9:16", "adaptive"], "ratio");
  b.content = content;
  delete b.prompt;
  delete b.first_frame_image;
  delete b.last_frame_image;
  let tokens, path, queryPath;
  if (vega) {
    fields(b, ["content", "duration", "resolution", "ratio", "options"]);
    const rates = model.endsWith("1.5-lite")
      ? {
          "480P": 27000,
          "720P": 54000,
          "1080P": 108000,
          "2K": 129600,
          "4K": 155500,
        }
      : { "768P": 37500, "1080P": 50000, "2K": 62500, "4K": 75000 };
    choice(b.resolution, Object.keys(rates), "resolution");
    if (b.options !== undefined) {
      fields(b.options, ["return_last_frame"]);
      bools(b.options, ["return_last_frame"]);
    }
    const inputRate = model.endsWith("1.5-lite") ? Math.min(rates[b.resolution], 108000) : rates[b.resolution];
    tokens = b.duration * rates[b.resolution] + (videos ? 45 * inputRate : 0) + (model.endsWith("1.0-lite") ? Math.max(0, images - 5) * 15000 : 0);
    path = "/v1/wand/vega-videos/tasks";
    queryPath = path + "/";
  } else if (h3) {
    fields(b, ["content", "duration", "resolution", "ratio", "extra", "aigc_watermark"]);
    choice(b.resolution, model.endsWith("h3-max") ? ["480P", "768P"] : ["768P", "2K"], "resolution");
    if (b.extra !== undefined) {
      if (!model.endsWith("h3-max")) throw Error("extra is only supported by H3 Max");
      fields(b.extra, ["prompt_expansion_mode"]);
      choice(b.extra.prompt_expansion_mode, ["disabled", "balanced", "quality"], "prompt_expansion_mode");
    }
    if (images > 9) throw Error("H3 supports at most 9 images");
    if (first) b.ratio = "adaptive";
    const rate = model.endsWith("h3-max") ? (b.resolution === "480P" ? 33000 : 50000) : b.resolution === "2K" ? 80000 : 50000;
    tokens = rate * (b.duration + (videos && !model.endsWith("h3-max") ? 15 : 0)) + (model.endsWith("h3-max") ? 0 : Math.max(0, images - 5) * 20000);
    path = "/v1/wand/minimax-video-v2/generation";
    queryPath = "/v1/wand/minimax-video-v2/tasks/";
  } else if (old) {
    fields(b, ["content", "duration", "resolution", "ratio", "prompt_optimizer", "fast_pretreatment", "aigc_watermark"]);
    choice(b.resolution, ["768P", "1080P"], "resolution");
    if (![6, 10].includes(b.duration) || (b.resolution === "1080P" && b.duration !== 6)) throw Error("v2.3 supports 6s/10s at 768P or 6s at 1080P");
    if (refs || videos || audios || (model.endsWith("fast") && (!first || last)))
      throw Error("v2.3 Fast requires a single first frame; references are unsupported");
    b.prompt = texts.join("\n");
    for (const item of content)
      if (item.role === "first_frame") b.first_frame_image = item.image_url.url;
      else if (item.role === "last_frame") b.last_frame_image = item.image_url.url;
    delete b.content;
    delete b.ratio;
    tokens = model.endsWith("fast")
      ? b.duration * (b.resolution === "1080P" ? 38500 : 22500)
      : b.resolution === "1080P"
        ? 350000
        : b.duration === 10
          ? 400000
          : 200000;
    path = "/v1/wand/minimax-video/generation";
    queryPath = "/v1/wand/minimax-video/tasks/";
  } else if (kling) {
    fields(b, ["content", "duration", "resolution", "ratio"]);
    choice(b.resolution, ["720P"], "resolution");
    if (last || audios || videos > 1) throw Error("Kling Flash accepts a first frame, reference images and one reference video, but no last frame or audio");
    const settings = {
      resolution: "720p",
      duration: b.duration,
      aspect_ratio: b.ratio,
    };
    const contents = content.map((item) =>
      item.type === "text"
        ? { type: "prompt", text: item.text }
        : {
            type: item.type === "video_url" ? "base_video" : item.role === "first_frame" ? "first_frame" : "refer_image",
            url: item[item.type].url,
          }
    );
    const payload = { model: model, settings: settings };
    if (!images && !videos) payload.prompt = texts.join("\n");
    else payload.contents = contents;
    tokens = videos ? 60000 * (b.duration + Math.max(15.5, b.duration)) : 100000 * b.duration;
    path = "/v1/wand/kling/" + (!images && !videos ? "text-to-video" : refs || videos ? "omni-video" : "image-to-video");
    queryPath = "/v1/wand/kling/tasks/";
    return {
      body: payload,
      tokens: tokens,
      path: path,
      queryPath: queryPath,
      duration: b.duration,
      resolution: b.resolution,
      hasVideo: videos > 0,
    };
  } else throw Error("Unsupported video model");
  return {
    body: b,
    tokens: tokens,
    path: path,
    queryPath: queryPath,
    duration: b.duration,
    resolution: b.resolution,
    hasVideo: videos > 0,
  };
}
function utf8Length(value) {
  return Array.from(value).reduce((n, c) => n + (c.codePointAt(0) <= 0x7f ? 1 : c.codePointAt(0) <= 0x7ff ? 2 : c.codePointAt(0) <= 0xffff ? 3 : 4), 0);
}
function audioRequest(model, input) {
  const b = Object.assign({}, input, { model: model });
  let path, tokens;
  if (ASR.includes(model)) {
    const tenlai = model === "tenlai-asr-v1.0";
    const asynchronous = tenlai || b["async"] === true;
    fields(b, tenlai ? ["data", "input_url"] : asynchronous ? ["input_url", "source", "subtitle_format", "async"] : ["data", "input_url", "source", "voice_encode_format", "async"]);
    if (b["async"] !== undefined && typeof b["async"] !== "boolean") throw Error("async must be boolean");
    delete b["async"];
    if (Boolean(b.data) === Boolean(b.input_url)) throw Error("Provide data or input_url");
    if (b.input_url) url(b.input_url);
    if (b.data && (typeof b.data !== "string" || b.data.length > 900000)) throw Error("Use input_url for large audio");
    if (b.subtitle_format !== undefined) choice(b.subtitle_format, ["srt", "vtt"], "subtitle_format");
    const rate = model === "wand-asr-v1" ? 50 : 22;
    tokens = asynchronous ? (tenlai ? 18000 : 36000) * rate : model === "hy-asr-3.0-preview" ? 90000 : 1800 * rate;
    path = tenlai ? "/v1/tenlai/asrproxy/create_rec_task" : asynchronous ? "/v1/wand/asrproxy/async_create" : "/v1/wand/asrproxy/sync_transcribe";
    return { body: b, tokens, path, sync: !asynchronous, queryPath: tenlai ? "/v1/tenlai/asrproxy/describe_task_status" : "/v1/wand/asrproxy/tasks/" };
  } else if (model === "tenlai-tts-v1.0") {
    fields(b, ["text", "voice_setting", "audio_setting", "output_format"]);
    prompt(b.text, 300);
    // Tenlai allows 300 UTF-8 bytes: 100 Chinese characters or 300 ASCII letters.
    if (utf8Length(b.text) > 300) throw Error("Tenlai text exceeds 300 UTF-8 bytes");
    if (!object(b.voice_setting)) throw Error("voice_setting.voice_id is required");
    fields(b.voice_setting, ["voice_id", "speed", "volume"]);
    const voice = b.voice_setting.voice_id;
    if (!(typeof voice === "string" && /^\d+$/.test(voice)) && !(Number.isSafeInteger(voice) && voice > 0)) throw Error("Tenlai voice_id must be a numeric voice ID");
    if (b.voice_setting.speed !== undefined) number(b.voice_setting.speed, -2, 6, "speed");
    if (b.voice_setting.volume !== undefined) number(b.voice_setting.volume, -10, 10, "volume");
    if (b.audio_setting !== undefined) {
      fields(b.audio_setting, ["sample_rate"]);
      if (b.audio_setting.sample_rate !== undefined) choice(b.audio_setting.sample_rate, [8000, 16000, 24000], "sample_rate");
    }
    if (b.output_format !== undefined) choice(b.output_format, ["mp3", "pcm"], "output_format");
    tokens = utf8Length(b.text) * 25;
    path = "/v1/tenlai/ttsproxy/text_to_voice";
  } else if (MINIMAX_TTS.includes(model)) {
    fields(b, [
      "text",
      "voice_setting",
      "audio_setting",
      "pronunciation_dict",
      "timbre_weights",
      "language_boost",
      "voice_modify",
      "subtitle_enable",
      "subtitle_type",
      "output_format",
      "aigc_watermark",
      "stream",
    ]);
    prompt(b.text, 9999);
    if (!object(b.voice_setting)) throw Error("voice_setting is required");
    if (b.timbre_weights !== undefined) {
      if (!Array.isArray(b.timbre_weights) || !b.timbre_weights.length || b.timbre_weights.length > 4) throw Error("Provide 1–4 timbre weights");
      for (const voice of b.timbre_weights) {
        fields(voice, ["voice_id", "weight"]);
        if (!text(voice.voice_id)) throw Error("timbre voice_id is required");
        integer(voice.weight, 1, 100, "timbre weight");
      }
    } else if (!text(b.voice_setting.voice_id)) throw Error("voice_setting.voice_id is required");
    if (b.stream) throw Error("Use non-streaming speech on this task endpoint");
    b.stream = false;
    b.output_format = "url";
    tokens = model === "minimax-speech-2.8-hd" ? Array.from(b.text).length * 70 : utf8Length(b.text) * (model.endsWith("turbo") ? 20 : 35);
    path = "/v1/wand/minimax-tts/sync_tts";
  } else if (model === "indextts-2") {
    fields(b, ["text", "voice", "audio_setting", "output_format"]);
    prompt(b.text, 9999);
    url(b.voice);
    if (b.audio_setting !== undefined) {
      fields(b.audio_setting, ["format", "sample_rate"]);
      if (b.audio_setting.format !== undefined) choice(b.audio_setting.format, ["wav", "mp3", "pcm", "flac"], "audio format");
      if (b.audio_setting.sample_rate !== undefined) integer(b.audio_setting.sample_rate, 8000, 48000, "sample_rate");
    }
    if (b.output_format !== undefined) choice(b.output_format, ["url", "hex"], "output_format");
    b.output_format = b.output_format || "url";
    // The console bills IndexTTS by UTF-8 byte, not JavaScript code unit.
    tokens = utf8Length(b.text) * 30;
    path = "/v1/wand/tts/sync_tts";
  } else if (model === "minimax-music-v3.0") {
    fields(b, ["prompt", "lyrics", "lyrics_optimizer", "is_instrumental", "audio_setting", "aigc_watermark", "output_format"]);
    bools(b, ["lyrics_optimizer", "is_instrumental", "aigc_watermark"]);
    if (b.prompt !== undefined) prompt(b.prompt, 2000);
    if (b.lyrics !== undefined) prompt(b.lyrics, 3500);
    if (b.is_instrumental || b.lyrics_optimizer) {
      prompt(b.prompt, 2000);
    } else prompt(b.lyrics, 3500);
    b.output_format = "url";
    tokens = 100000;
    path = "/v1/wand/minimax-music/generation";
  } else throw Error("Unsupported audio model");
  return { body: b, tokens: tokens, path: path, sync: true };
}
function hy3dRequest(model, input) {
  fields(input, ["prompt", "image_url", "image_base64", "multi_view_images", "enable_pbr", "face_count", "generate_type", "result_format"]);
  const b = Object.assign({}, input, { model });
  const type = choice(b.generate_type === undefined ? "Normal" : b.generate_type, ["Normal", "Geometry"], "generate_type");
  const hasPrompt = b.prompt !== undefined;
  const hasURL = b.image_url !== undefined;
  const hasBase64 = b.image_base64 !== undefined;
  if (Number(hasPrompt) + Number(hasURL) + Number(hasBase64) !== 1) throw Error("Provide exactly one of prompt, image_url or image_base64");
  if (hasPrompt) prompt(b.prompt, 1024);
  if (hasURL && (typeof b.image_url !== "string" || !/^https?:\/\//.test(b.image_url))) throw Error("image_url must be an HTTP(S) URL");
  if (hasURL) url(b.image_url);
  if (hasBase64) {
    const value = b.image_base64;
    if (
      typeof value !== "string" ||
      !value ||
      value.length > 8 * 1024 * 1024 ||
      value.length % 4 !== 0 ||
      !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value)
    )
      throw Error("image_base64 must be valid base64 within 6 MiB decoded");
    const bytes = (value.length / 4) * 3 - (value.endsWith("==") ? 2 : value.endsWith("=") ? 1 : 0);
    if (bytes > 6 * 1024 * 1024) throw Error("image_base64 exceeds 6 MiB");
  }
  bools(b, ["enable_pbr"]);
  if (b.face_count !== undefined) {
    integer(b.face_count, 3000, 1500000, "face_count");
    // Omit an explicit default so it cannot select the paid custom-face option.
    if (b.face_count === 500000) delete b.face_count;
  }
  if (b.multi_view_images !== undefined) {
    if (!Array.isArray(b.multi_view_images) || b.multi_view_images.length > 7) throw Error("multi_view_images accepts up to 7 supplementary views");
    if (hasPrompt && b.multi_view_images.length) throw Error("multi_view_images requires image input");
    const seen = [];
    b.multi_view_images = b.multi_view_images.map((item) => {
      fields(item, ["view_type", "view_image_url"]);
      choice(item.view_type, ["left", "right", "back", "top", "bottom", "left_front", "right_front"], "view_type");
      if (seen.includes(item.view_type)) throw Error("Duplicate view_type");
      seen.push(item.view_type);
      if (typeof item.view_image_url !== "string" || !/^https?:\/\//.test(item.view_image_url)) throw Error("view_image_url must be an HTTP(S) URL");
      url(item.view_image_url);
      return { view_type: item.view_type, view_image_url: item.view_image_url };
    });
    if (!b.multi_view_images.length) delete b.multi_view_images;
  }
  if (b.result_format !== undefined) choice(b.result_format, ["stl", "usdz", "fbx"], "result_format");
  b.generate_type = type;
  // Geometry has no texture: PBR is ineffective and must not add a fee.
  if (type === "Geometry") b.enable_pbr = false;
  const usage = {
    model_count: 1,
    generate_type: type,
    pbr: type === "Normal" && b.enable_pbr === true ? 1 : 0,
    custom_face_count: b.face_count !== undefined ? 1 : 0,
    multi_view: b.multi_view_images ? 1 : 0,
    extra_format: b.result_format !== undefined ? 1 : 0,
  };
  return { body: b, usage, path: "/v1/api/3d/submit", queryPath: "/v1/api/3d/query" };
}
function hy3dFiles(b) {
  if (!Array.isArray(b.data)) return [];
  return b.data.flatMap((item, i) => {
    if (!object(item)) return [];
    const result = [];
    const download = https(item.url);
    const preview = https(item.preview_image_url);
    if (download) result.push({ key: "model-" + i, type: "file", url: download, mimeType: "" });
    if (preview) result.push({ key: "preview-" + i, type: "image", url: preview, mimeType: "" });
    return result;
  });
}

function normalized(model, body) {
  if (!MODELS.includes(model)) throw Error("Unsupported TokenHub media model");
  if (model === HY3D_MODEL) return hy3dRequest(model, body);
  return IMAGES.includes(model) ? imageRequest(model, body) : VIDEOS.includes(model) ? videoRequest(model, body) : audioRequest(model, body);
}
function request(ctx, method, path, body) {
  const r = {
    method: method,
    url: ctx.baseUrl.replace(/\/+$/, "") + path,
    headers: {
      Authorization: "Bearer " + ctx.apiKey,
      "Content-Type": "application/json",
    },
  };
  if (body !== undefined) r.body = body;
  return r;
}
export function buildSubmitRequest(ctx) {
  const n = normalized(modelOf(ctx), ctx.requestBody);
  return request(ctx, "POST", n.path, n.body);
}
export function extractUsage(ctx) {
  if (ctx.usagePurpose === "billing_ratios") return null;
  const n = normalized(modelOf(ctx), ctx.requestBody);
  return modelOf(ctx) === HY3D_MODEL ? n.usage : { tokens: n.tokens };
}
function failed(b) {
  return !!(
    b.error ||
    (b.ErrCode !== undefined && b.ErrCode !== 0) ||
    (b.code !== undefined && b.code !== 0) ||
    (b.base_resp && b.base_resp.status_code !== undefined && b.base_resp.status_code !== 0)
  );
}
function hasTranscription(b) {
  return !!b.output && (typeof b.output.text === "string" || !!https(b.output.subtitle_url));
}
function audioValue(model, b) {
  return model === "indextts-2" ? b.output && b.output.data : model === "tenlai-tts-v1.0" ? b.data && b.data.audio_url : b.data && b.data.audio;
}
function audioPayload(model, b) {
  const value = audioValue(model, b);
  return https(value) || (typeof value === "string" && value.length > 0 &&
    (model === "tenlai-tts-v1.0" ? value.length % 4 === 0 && /^[A-Za-z0-9+/]+={0,2}$/.test(value) : value.length % 2 === 0 && /^[0-9a-f]+$/i.test(value)));
}
function measuredTokens(model, b, state) {
  let n = b.tokenhub_usage && b.tokenhub_usage.total_tokens;
  if (n === undefined && b.usage) n = b.usage.total_tokens === undefined ? b.usage.total_token : b.usage.total_tokens;
  if (n !== undefined) return number(n, 0, 1000000000, "TokenHub measured tokens");
  if (model === "tenlai-asr-v1.0") return number(b.audio_duration, 0, 18000, "audio_duration") * 22;
  // Tenlai synchronous TTS documents no usage; its input is billed by UTF-8 length.
  if (model === "tenlai-tts-v1.0" && state) return number(state.tokens, 0, 7500, "Tenlai text tokens");
  // WAND's documented query omits metering for simple fixed-duration jobs.
  // Reference-video jobs need measured input usage; never settle their maximum reservation.
  if ((model === "indextts-2" || model.startsWith("wand-vega-video")) && state && !state.hasVideo) return state.tokens;
  throw Error("TokenHub measured token usage is missing");
}
function imageData(b) {
  const data = Array.isArray(b.data) ? b.data : object(b.data) ? [b.data] : [];
  return data.filter((x) => object(x) && (https(x.url) || text(x.b64_json)));
}
function videoURL(model, b) {
  model = model || (b.task ? "minimax-video-h3" : "");
  if (model.startsWith("pixverse-video-")) return https(b.Resp && b.Resp.url);
  if (model === "kling-video-v4-flash" || Array.isArray(b.data)) {
    const d = Array.isArray(b.data) ? b.data[0] : b.data;
    const f = d && Array.isArray(d.outputs) && d.outputs.find((x) => x.type === "video");
    return https(f && f.url);
  }
  const node = model.startsWith("minimax-video-h3") ? b.task : b;
  return https(node && node.content && (node.content.url || node.content.video_url));
}
export function parseSubmitResponse(ctx, resp) {
  const b = resp.body || {},
    model = modelOf(ctx),
    n = normalized(model, ctx.requestBody);
  const state = {
    tokens: n.tokens,
    duration: n.duration || 0,
    resolution: n.resolution || "",
    hasVideo: !!n.hasVideo,
    queryPath: n.queryPath || "",
    model: model,
  };
  if (model === HY3D_MODEL) {
    if (failed(b) || b.status === "failed") return { taskId: ctx.publicTaskId, immediate: { status: "FAILURE", reason: "TokenHub 3D generation failed" } };
    const id = text(b.id);
    if (!id) throw Error("TokenHub returned no 3D task ID");
    const result = { taskId: id, taskData: b, state: { model, usage: n.usage } };
    if (b.status === "completed") {
      if (!hy3dFiles(b).some((item) => item.type === "file")) throw Error("TokenHub completed without a 3D model");
      result.immediate = { status: "SUCCESS" };
    }
    return result;
  }
  if (failed(b) || b.status === "failed" || (model === "tenlai-tts-v1.0" && b.status === 3))
    return {
      taskId: ctx.publicTaskId,
      immediate: {
        status: "FAILURE",
        reason: "TokenHub rejected the media request",
      },
    };
  if (n.sync) {
    measuredTokens(model, b, state);
    if (IMAGES.includes(model) && !imageData(b).length) throw Error("TokenHub returned no images");
    const audioComplete = model === "tenlai-tts-v1.0" ? b.status === "completed" || b.status === 2 :
      model === "indextts-2" ? b.status === "completed" : b.data && (b.data.status === undefined || b.data.status === 2);
    if (AUDIO.includes(model) && !ASR.includes(model) && (!audioComplete || !audioPayload(model, b))) {
      throw Error("TokenHub returned no audio URL");
    }
    if (ASR.includes(model) && !hasTranscription(b)) throw Error("TokenHub returned no transcription");
    return {
      taskId: ctx.publicTaskId,
      taskData: b,
      state: state,
      immediate: { status: "SUCCESS" },
    };
  }
  if (ASR.includes(model)) {
    const id = typeof b.id === "string" ? text(b.id) : Number.isSafeInteger(b.id) && b.id > 0 ? String(b.id) : "";
    if (!id) throw Error("TokenHub returned no ASR task ID");
    const result = { taskId: id, taskData: b, state };
    if (model === "tenlai-asr-v1.0") state.providerTaskId = b.id;
    if (b.status === "completed") {
      measuredTokens(model, b, state);
      if (!hasTranscription(b)) throw Error("TokenHub returned no transcription");
      result.immediate = { status: "SUCCESS" };
    }
    return result;
  }
  const task = b.Resp || b.task || b.data || b,
    id = text(task.video_id || task.task_id || task.id || b.task_id);
  if (!id) throw Error("TokenHub returned no task ID");
  if (IMAGES.includes(model)) state.queryPath = "/v1/wand/vega-images/tasks/";
  return { taskId: id, taskData: b, state: state };
}
export function buildQueryRequest(ctx) {
  if (modelOf(ctx) === "tenlai-asr-v1.0") {
    if (!ctx.state || ctx.state.providerTaskId === undefined) throw Error("Missing Tenlai task ID");
    return request(ctx, "POST", "/v1/tenlai/asrproxy/describe_task_status", { model: "tenlai-asr-v1.0", task_id: ctx.state.providerTaskId });
  }
  if (modelOf(ctx) === HY3D_MODEL) return request(ctx, "POST", "/v1/api/3d/query", { model: HY3D_MODEL, id: ctx.taskId });
  if (!ctx.state || !ctx.state.queryPath) throw Error("Missing TokenHub task query path");
  return request(ctx, "GET", ctx.state.queryPath + encodeURIComponent(ctx.taskId));
}
export function parseTaskResult(ctx, b) {
  if (!object(b)) return { status: "UNKNOWN" };
  if (failed(b)) return { status: "FAILURE", reason: "TokenHub media generation failed" };
  const model = modelOf(ctx);
  if (model.startsWith("pixverse-video-")) {
    const status = b.Resp && { 1: "SUCCESS", 5: "IN_PROGRESS", 6: "FAILURE", 7: "FAILURE", 8: "FAILURE" }[b.Resp.status];
    if (status === "SUCCESS") {
      measuredTokens(model, b, ctx.state);
      if (!videoURL(model, b)) throw Error("TokenHub completed without a media result");
    }
    return {
      status: status || "UNKNOWN",
      ...(status === "FAILURE"
        ? { reason: { 6: "PixVerse result was deleted", 7: "PixVerse content moderation failed", 8: "PixVerse generation failed" }[b.Resp.status] }
        : {}),
    };
  }
  const node = model === "kling-video-v4-flash" ? (Array.isArray(b.data) ? b.data[0] : b.data) : model.startsWith("minimax-video-h3") ? b.task : b;
  const statuses = {
    queued: "QUEUED",
    submitted: "QUEUED",
    Preparing: "QUEUED",
    Queueing: "QUEUED",
    running: "IN_PROGRESS",
    processing: "IN_PROGRESS",
    in_progress: "IN_PROGRESS",
    Processing: "IN_PROGRESS",
    completed: "SUCCESS",
    succeeded: "SUCCESS",
    Success: "SUCCESS",
    failed: "FAILURE",
    Fail: "FAILURE",
    cancelled: "FAILURE",
    incomplete: "FAILURE",
  };
  const status = (node && statuses[node.status]) || "UNKNOWN";
  if (status === "SUCCESS" && model === HY3D_MODEL) {
    if (!hy3dFiles(b).some((item) => item.type === "file")) throw Error("TokenHub completed without a 3D model");
  } else if (status === "SUCCESS") {
    measuredTokens(model, b, ctx.state);
    if (ASR.includes(model) ? !hasTranscription(b) : IMAGES.includes(model) ? !imageData(b).length : !videoURL(model, b)) throw Error("TokenHub completed without a media result");
  }
  return {
    status: status,
    ...(status === "FAILURE"
      ? {
          reason: text(node.message) || text(node.error && node.error.message) || "TokenHub media generation failed",
        }
      : {}),
  };
}
export function extractUsageOnComplete(ctx, result, b) {
  if (modelOf(ctx) === HY3D_MODEL) {
    if (result.status !== "SUCCESS") return null;
    if (!ctx.state || !object(ctx.state.usage)) throw Error("Missing 3D billing facts");
    return ctx.state.usage;
  }
  return result.status === "SUCCESS" ? { tokens: measuredTokens(modelOf(ctx), b, ctx.state) } : null;
}
function files(model, b) {
  model = model || "";
  if (model === HY3D_MODEL) return hy3dFiles(b);
  if (IMAGES.includes(model))
    return imageData(b)
      .filter((x) => https(x.url))
      .map((x, i) => ({
        key: "image-" + i,
        type: "image",
        url: x.url,
        mimeType: "",
      }));
  if (VIDEOS.includes(model)) {
    const u = videoURL(model, b);
    return u ? [{ key: "video", type: "video", url: u, mimeType: "video/mp4" }] : [];
  }
  if (ASR.includes(model)) {
    const u = https(b.output && b.output.subtitle_url);
    return u
      ? [
          {
            key: "subtitles",
            type: "file",
            mimeType: /\.vtt(?:\?|$)/i.test(u) ? "text/vtt" : "application/x-subrip",
            url: u,
          },
        ]
      : [];
  }
  const u = https(audioValue(model, b));
  if (!u) return [];
  const artifacts = [{ key: "audio", type: "audio", mimeType: "", url: u }];
  const subtitles = https(b.data && b.data.subtitle_file);
  if (subtitles) artifacts.push({ key: "subtitles", type: "file", mimeType: "application/json", url: subtitles });
  return artifacts;
}
export function listArtifacts(task) {
  if (task.status !== "SUCCESS") return [];
  const model =
    (task.state && task.state.model) ||
    task.upstreamModel ||
    task.model ||
    (task.properties && (task.properties.upstream_model_name || task.properties.origin_model_name));
  return files(model, task.data || {}).map((x) => ({
    key: x.key,
    type: x.type,
    mimeType: x.mimeType,
  }));
}
export function buildContentRequest(ctx) {
  const f = files(modelOf(ctx), ctx.data || {}).find((x) => x.key === ctx.artifactKey);
  if (!f) throw Error("artifact_not_found");
  return { method: ctx.clientRequest.method, url: f.url, credentialless: true };
}
function create(ctx) {
  if (!ctx.body || ctx.body.kind !== "json" || !object(ctx.body.value)) throw Error("JSON body required");
  const b = ctx.body.value,
    model = modelOf(ctx) || b.model;
  normalized(model, b);
  return {
    kind: "submit",
    model: ctx.model || b.model,
    action:
      model === HY3D_MODEL
        ? "generate_3d"
        : IMAGES.includes(model)
          ? "generate_image"
          : VIDEOS.includes(model)
            ? "generate_video"
            : ASR.includes(model)
              ? "transcribe_audio"
              : "generate_audio",
    requestBody: b,
  };
}
function miniStatus(ctx, task) {
  const state =
    {
      NOT_START: "queued",
      SUBMITTED: "queued",
      QUEUED: "queued",
      IN_PROGRESS: "running",
      SUCCESS: "succeeded",
      FAILURE: "failed",
    }[task.status] || "unknown";
  const t = {
    id: task.task_id,
    model: ctx.model,
    status: state,
    created_at: task.created_at,
    updated_at: task.updated_at || task.created_at,
    task_type: "generation",
    modality: "video",
  };
  if (task.status === "SUCCESS") {
    t.content = { url: videoURL(modelOf(ctx), task.data || {}) };
    const node = (task.data || {}).task || task.data || {};
    if (node.duration !== undefined) t.duration = node.duration;
    if (node.resolution) t.resolution = node.resolution;
    if (node.usage) t.usage = node.usage;
  }
  if (task.status === "FAILURE") t.error = { message: task.fail_reason };
  return { task: t };
}
export const native = {
  create: create,
  task(ctx, task) {
    const out = { task_id: task.task_id, status: task.status };
    if (task.status === "SUCCESS") {
      out.result = task.data;
      out.artifacts = "/v1/tasks/" + encodeURIComponent(task.task_id) + "/artifacts";
    }
    if (task.status === "FAILURE") out.error = task.fail_reason;
    return out;
  },
};
function imageDecode(ctx) {
  let b;
  if (ctx.body && ctx.body.kind === "json") b = Object.assign({}, ctx.body.value);
  else if (ctx.body && ctx.body.kind === "multipart") {
    b = {};
    for (const k of Object.keys(ctx.body.fields || {})) {
      const values = ctx.body.fields[k];
      if (values.length !== 1) throw Error("Duplicate field: " + k);
      b[k] = values[0];
    }
    if (b.n !== undefined) b.n = Number(b.n);
    b.images = (ctx.body.files || [])
      .filter((f) => f.field === "image" || f.field === "image[]")
      .map((f) => ({
        __fileRef: f.ref,
        encoding: "dataUrl",
        maxBytes: 20 * 1024 * 1024,
      }));
    if ((ctx.body.files || []).some((f) => f.field !== "image" && f.field !== "image[]")) throw Error("Only image uploads are supported");
  } else throw Error("JSON or multipart required");
  if (b.image !== undefined) {
    if (b.images !== undefined) throw Error("Use image or images");
    b.images = Array.isArray(b.image) ? b.image : [b.image];
    delete b.image;
  }
  const model = modelOf(ctx) || b.model;
  normalized(model, b);
  return {
    kind: "submit",
    model: ctx.model || b.model,
    action: "generate_image",
    requestBody: b,
  };
}
function videoDecode(ctx) {
  if (!ctx.body || !["json", "multipart"].includes(ctx.body.kind)) throw Error("JSON or multipart required");
  let value;
  if (ctx.body.kind === "json") value = ctx.body.value;
  else {
    value = {};
    for (const k of Object.keys(ctx.body.fields || {})) {
      if (ctx.body.fields[k].length !== 1) throw Error("Duplicate field: " + k);
      value[k] = ctx.body.fields[k][0];
    }
    if (value.metadata) value.metadata = JSON.parse(value.metadata);
    const f = ctx.body.files || [];
    if (f.length > 1 || f.some((x) => x.field !== "input_reference")) throw Error("Only one input_reference upload is supported");
    if (f[0])
      value.input_reference = {
        __fileRef: f[0].ref,
        encoding: "dataUrl",
        maxBytes: 20 * 1024 * 1024,
      };
  }
  fields(value, ["prompt", "seconds", "duration", "resolution", "size", "aspect_ratio", "input_reference", "metadata"]);
  if (value.metadata !== undefined && !object(value.metadata)) throw Error("metadata must be an object");
  const b = Object.assign({}, value.metadata || {}, { model: ctx.model });
  if (value.prompt !== undefined) b.prompt = value.prompt;
  if (value.seconds !== undefined) b.duration = Number(value.seconds);
  if (value.duration !== undefined) b.duration = Number(value.duration);
  if (value.resolution) b.resolution = value.resolution;
  if (value.aspect_ratio) b.ratio = value.aspect_ratio;
  if (value.size) {
    const m = /^(\d+)x(\d+)$/.exec(value.size);
    if (!m) throw Error("size must be WIDTHxHEIGHT");
    const short = Math.min(Number(m[1]), Number(m[2]));
    b.resolution = short + "P";
    if (short === 1440) b.resolution = "2K";
    if (short === 2160) b.resolution = "4K";
  }
  if (value.input_reference) {
    if (b.content) throw Error("Use content or input_reference");
    b.first_frame_image = value.input_reference;
  }
  normalized(modelOf(ctx), b);
  return {
    kind: "submit",
    model: ctx.model,
    action: "generate_video",
    requestBody: b,
  };
}
export const protocols = {
  openai_image: {
    decodeRequest: imageDecode,
    render(ctx, task) {
      return { data: imageData(task.data || {}) };
    },
  },
  minimax_video: { decodeRequest: create, render: miniStatus },
  openai_video: {
    decodeRequest: videoDecode,
    render(ctx, task) {
      return {
        url: task.status === "SUCCESS" ? videoURL(modelOf(ctx), task.data || {}) : undefined,
      };
    },
  },
};
