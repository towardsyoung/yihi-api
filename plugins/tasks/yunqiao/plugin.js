export const meta = {
  apiVersion: 1,
  key: "yunqiao",
  name: "云桥视频",
  icon: "text:云桥",
  description: {
    en: "Yunqiao asynchronous text-to-video and image-to-video generation",
    zh: "云桥异步文生视频和图生视频",
  },
  version: "1.2.0",
  author: { name: "QuantumNous" },
  website: "https://nextrun.top/docs#videos",
  baseUrl: "https://asiasouth.up.railway.app",
  models: ["seedance-2-0-fast-NSFW", "seedance-2.0-hd-cloud", "MiniMax-H3", "seedance-2-0-mini-NSFW", "kling-3.0-turbo", "seedance-2.5-self-developed-NSFW", "seedance-2.0-fast-cloud", "seedance-2-0-mini", "grok-imagine-1.5", "gemini-omni-1.1-flash", "seedance-2.5", "minimax-h3-lite", "kling-v4", "seedance-2.0-mini-cloud", "wan3.0-video", "muse-video", "kling-v3"],
  fetchMode: "per_task",
  usageSchema: {
    seconds: {
      type: "number",
      unit: "second",
      description: { en: "Video generation unit price", zh: "视频生成单价" },
    },
  },
  usageProfiles: [{
    models: ["MiniMax-H3"],
    schema: {
      seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
      resolution: { enum: ["480p", "768p", "1080p"], description: { en: "Output video resolution", zh: "输出视频分辨率" } },
      video_input: { enum: ["none", "video"], description: { en: "Reference video input", zh: "参考视频输入" } },
    },
  }],
  protocols: [{ name: "openai_responses", supports: ["stream", "sync", "background"] }, "openai_video"],
};

// Keep the reservation and outbound duration identical, including omitted duration.
// 3600 is the host MaxTaskDurationSeconds bound; model-specific limits remain upstream-owned.
function normalizeRequest(req, model) {
  if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
  if (typeof req.prompt !== "string" || !req.prompt.trim()) throw new Error("prompt is required");
  for (const name of ["seconds", "duration"]) {
    const value = req[name];
    if (value !== undefined && ((typeof value !== "number" && typeof value !== "string") || !String(value).trim() ||
        !Number.isInteger(Number(value)) || Number(value) < 1 || Number(value) > 3600))
      throw new Error(name + " must be an integer between 1 and 3600");
  }
  if (req.seconds !== undefined && req.duration !== undefined && Number(req.seconds) !== Number(req.duration))
    throw new Error("seconds and duration must match");
  const values = Object.assign({}, req, { model: model, seconds: Number(req.seconds === undefined ? (req.duration === undefined ? 5 : req.duration) : req.seconds) });
  delete values.duration;
  if (values.images !== undefined && values.image_urls !== undefined) throw new Error("provide either images or image_urls");
  let images = values.image_urls === undefined ? values.images : values.image_urls;
  if (images !== undefined && (!Array.isArray(images) || images.some(function (image) { return typeof image !== "string" || !image.trim(); })))
    throw new Error("image_urls must be an array of non-empty image URLs");
  if (values.input_reference !== undefined && values.image !== undefined) throw new Error("provide reference images through one field");
  const singleImage = values.input_reference === undefined ? values.image : values.input_reference;
  if (singleImage !== undefined) {
    if (typeof singleImage !== "string" || !singleImage.trim()) throw new Error("input_reference must be a non-empty string; upload files with multipart");
    if (images && images.length) throw new Error("provide reference images through one field");
    images = [singleImage];
  }
  // Match the host image-count ceiling, not a claimed vendor/model limit.
  if (images && images.length > 128) throw new Error("at most 128 image URLs are allowed");
  if (images !== undefined) values.image_urls = images.slice();
  delete values.images;
  delete values.image;
  delete values.input_reference;
  if (values.metadata !== undefined) {
    const metadata = values.metadata;
    if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) throw new Error("metadata must be an object");
    if (values.resolution === undefined && metadata.resolution !== undefined) values.resolution = metadata.resolution;
    if (values.ratio === undefined && metadata.aspect_ratio !== undefined) values.ratio = metadata.aspect_ratio;
    if (values.generate_audio === undefined && metadata.generate_audio !== undefined) values.generate_audio = metadata.generate_audio;
    if (metadata.audios !== undefined && (!Array.isArray(metadata.audios) || metadata.audios.length))
      throw new Error("Yunqiao reference audio is not supported");
  }
  return values;
}

function trimmed(value) {
  return String(value || "").trim();
}

function responsesInput(req) {
  const texts = [],
    images = [];
  const input = req.input;
  if (typeof input === "string") texts.push(input);
  else if (Array.isArray(input)) {
    for (const item of input) {
      if (typeof item === "string") {
        texts.push(item);
        continue;
      }
      if (!item || typeof item !== "object" || Array.isArray(item)) continue;
      const content = item.content === undefined ? [item] : Array.isArray(item.content) ? item.content : [item.content];
      for (const part of content) {
        if (typeof part === "string") {
          texts.push(part);
          continue;
        }
        if (!part || typeof part !== "object" || Array.isArray(part)) continue;
        if (["input_text", "text"].includes(part.type) && typeof part.text === "string") texts.push(part.text);
        if (["input_image", "image_url"].includes(part.type)) {
          let image = part.image_url;
          if (image && typeof image === "object") image = image.url;
          if (trimmed(image)) images.push(trimmed(image));
        }
      }
    }
  }
  return {
    prompt: texts
      .filter(function (text) {
        return trimmed(text);
      })
      .join("\n"),
    images: images,
  };
}

function responsesVideoText(ctx) {
  const artifact = ctx && ctx.artifacts && ctx.artifacts.video;
  const url = trimmed(artifact && artifact.url);
  if (!url) throw new Error("video artifact is unavailable");
  const escaped = url.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return '<video controls src="' + escaped + '"></video>';
}

export function buildSubmitRequest(ctx) {
  const req = normalizeRequest(ctx.requestBody, ctx.upstreamModel || ctx.model);
  // Match the shared host model spelling while using Yunqiao's published wire name.
  if (req.model === "MiniMax-H3") req.model = "minimax-h3";
  // Yunqiao's JSON API requires a string; normalized usage stays numeric.
  req.seconds = String(req.seconds);
  const headers = { Authorization: "Bearer " + ctx.apiKey };
  if ((ctx.files || []).length) {
    const parts = [];
    for (const key of Object.keys(req)) {
      if (req[key] === undefined || req[key] === null) continue;
      parts.push({ name: key, value: typeof req[key] === "object" ? JSON.stringify(req[key]) : String(req[key]) });
    }
    for (const file of ctx.files) parts.push({ name: "input_reference", fileRef: file.ref, filename: file.filename });
    return { url: ctx.baseUrl + "/v1/videos", method: "POST", headers, bodyType: "multipart", parts };
  }
  headers["Content-Type"] = "application/json";
  return { url: ctx.baseUrl + "/v1/videos", method: "POST", headers, body: req };
}

export function parseSubmitResponse(ctx, resp) {
  const body = resp.body || {};
  const taskId = body.id || body.task_id;
  if (!taskId) throw new Error("task_id is empty");
  const result = parseTaskResult(ctx, body);
  const parsed = { taskId, taskData: body };
  if (result.status === "SUCCESS" || result.status === "FAILURE") parsed.immediate = result;
  return parsed;
}

export function extractUsage(ctx) {
  const req = normalizeRequest(ctx.requestBody, ctx.upstreamModel || ctx.model);
  const facts = { seconds: req.seconds };
  if (String(ctx.upstreamModel || ctx.model).toLowerCase() === "minimax-h3" && ctx.usagePurpose !== "billing_ratios") {
    const metadata = req.metadata || {};
    const resolution = String(req.resolution || metadata.resolution || "").toLowerCase();
    if (!["480p", "768p", "1080p"].includes(resolution)) throw new Error("H3 resolution must be 480p, 768p, or 1080p");
    facts.resolution = resolution;
    facts.video_input = (metadata.content || []).some(function (part) { return part && part.type === "video_url"; }) ? "video" : "none";
  }
  return facts;
}

// Only translate parameters here; extractUsage remains the billing authority
// for both quote and submission. Placeholders are never sent or downloaded.
export function buildQuoteRequest(ctx) {
  const p = ctx.parameters;
  if (p.type !== "video") throw new Error("Yunqiao only supports video quotes");
  if (p.hasReferenceVideo) throw new Error("Yunqiao reference video quotes are not supported");
  const req = Object.assign({}, p.options || {}, { model: ctx.upstreamModel, prompt: "quote", seconds: p.duration });
  if (p.resolution) req.resolution = p.resolution;
  if (p.aspectRatio) req.ratio = p.aspectRatio;
  if (p.quality) req.quality = p.quality;
  if (p.size) req.size = p.size;
  if (p.audio !== undefined) req.generate_audio = p.audio;
  if (p.referenceImageCount) req.image_urls = Array.from({ length: p.referenceImageCount }, function () { return "https://quote.invalid/reference"; });
  return { requestBody: normalizeRequest(req, ctx.upstreamModel), action: p.referenceImageCount ? "image_to_video" : "text_to_video" };
}

export function extractUsageOnComplete(task, taskResult, body) {
  // Yunqiao usage.quota is vendor currency, never a local quota or duration.
  const value = (body || {}).seconds === undefined ? (body || {}).duration : body.seconds;
  const seconds = Number(value);
  return (typeof value === "number" || typeof value === "string") && Number.isInteger(seconds) && seconds >= 1 && seconds <= 3600 ? { seconds } : {};
}

export function buildQueryRequest(ctx) {
  return { url: ctx.baseUrl + "/v1/tasks/" + encodeURIComponent(ctx.taskId), method: "GET", headers: { Authorization: "Bearer " + ctx.apiKey } };
}

export function parseTaskResult(ctx, body) {
  const statuses = {
    queued: "QUEUED",
    pending: "QUEUED",
    processing: "IN_PROGRESS",
    in_progress: "IN_PROGRESS",
    completed: "SUCCESS",
    failed: "FAILURE",
    cancelled: "FAILURE",
  };
  body = body || {};
  const mapped = Object.prototype.hasOwnProperty.call(statuses, body.status) ? statuses[body.status] : undefined;
  const result = { status: mapped || "UNKNOWN" };
  if (!mapped) result.reason = "unrecognized status: " + String(body.status || "");
  if (body.progress > 0 && body.progress < 100) result.progress = body.progress + "%";
  if (result.status === "SUCCESS") {
    const videos = body.result && body.result.videos;
    const video = Array.isArray(videos) ? videos.find(function (item) { return item && typeof item.url === "string" && item.url.trim(); }) : null;
    if (video) result.url = video.url;
  }
  if (result.status === "FAILURE") result.reason = body.error && body.error.message ? body.error.message : "task failed";
  return result;
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  return {
    url: ctx.baseUrl + "/v1/videos/" + encodeURIComponent(ctx.upstreamTaskId) + "/content",
    method: ctx.clientRequest.method,
    headers: { Authorization: "Bearer " + ctx.apiKey },
  };
}

export const protocols = {
  openai_responses: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
      const req = ctx.body.value;
      if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
      const model = trimmed(req.model);
      if (!model) throw new Error("model is required");
      if (req.input !== undefined && typeof req.input !== "string" && !Array.isArray(req.input)) throw new Error("input must be a string or array");
      for (const key of ["images", "image_urls"]) {
        if (req[key] !== undefined && !Array.isArray(req[key])) throw new Error(key + " must be an array");
      }
      if (req.metadata !== undefined && (!req.metadata || typeof req.metadata !== "object" || Array.isArray(req.metadata)))
        throw new Error("metadata must be an object");
      const input = responsesInput(req);
      const prompt = input.prompt || trimmed(req.prompt);
      if (!prompt) throw new Error("input is required");
      const images = [];
      for (const image of [req.image, req.input_reference].concat(req.image_urls || [], req.images || [], input.images)) {
        if (image === undefined) continue;
        if (typeof image !== "string" || !image.trim()) throw new Error("reference images must be non-empty URL strings");
        if (!images.includes(image)) images.push(image);
      }
      const requestBody = { model: model, prompt: prompt };
      if (images.length) requestBody.image_urls = images;
      if (Object.prototype.hasOwnProperty.call(req, "seconds")) requestBody.seconds = req.seconds;
      if (Object.prototype.hasOwnProperty.call(req, "duration")) requestBody.duration = req.duration;
      for (const key of ["size", "ratio", "resolution", "quality", "generate_audio"]) {
        if (Object.prototype.hasOwnProperty.call(req, key)) requestBody[key] = req[key];
      }
      if (Object.prototype.hasOwnProperty.call(req, "metadata")) requestBody.metadata = req.metadata;
      return { kind: "submit", model: model, action: images.length ? "image_to_video" : "text_to_video", requestBody: normalizeRequest(requestBody, model) };
    },
    renderEvents: function (ctx, task, previousState) {
      const status = String(task.status || "UNKNOWN").toUpperCase();
      const value = Number(String(task.progress || "").replace("%", ""));
      const progress = Number.isFinite(value) && value >= 0 && value <= 100 ? value : null;
      const state = { status: status, progress: progress };
      if (status === "SUCCESS") {
        const text = responsesVideoText(ctx);
        const events = previousState && previousState.status === status ? [] : [{ type: "output", data: text }];
        return { events: events, state: state, done: true };
      }
      if (status === "FAILURE")
        return { events: [{ type: "error", code: "task_failed", message: task.fail_reason || "task failed" }], state: state, done: true };
      if (previousState && previousState.status === status && previousState.progress === progress) return { events: [], state: state, done: false };
      const event = { type: "progress", message: status.toLowerCase() };
      if (progress !== null) event.progress = progress;
      return { events: [event], state: state, done: false };
    },
    renderFinal: function (ctx, _task) {
      return {
        output: [
          {
            type: "message",
            status: "completed",
            role: "assistant",
            content: [{ type: "output_text", text: responsesVideoText(ctx), annotations: [], logprobs: [] }],
          },
        ],
        metadata: { vendor: "yunqiao" },
      };
    },
  },
};

protocols.openai_video = {
  decodeRequest: function (ctx) {
    if (!ctx.body || (ctx.body.kind !== "json" && ctx.body.kind !== "multipart")) throw new Error("JSON or multipart body required");
    if (ctx.body.kind === "json") {
      const req = normalizeRequest(ctx.body.value, ctx.model);
      return { kind: "submit", model: ctx.model, action: req.image_urls && req.image_urls.length ? "image_to_video" : "text_to_video", requestBody: req };
    }
    const first = function (name) {
      const values = (ctx.body.fields || {})[name] || [];
      if (values.length > 1) throw new Error(name + " must be provided once");
      return values[0];
    };
    const req = {};
    const fields = ctx.body.fields || {};
    for (const name of Object.keys(fields)) {
      req[name] = first(name);
    }
    let hasInputReferenceFile = false;
    for (const file of ctx.body.files || []) {
      if (file.field !== "input_reference") throw new Error("unexpected file field: " + file.field);
      if (hasInputReferenceFile) throw new Error("input_reference must be provided once");
      hasInputReferenceFile = true;
    }
    if (req.metadata !== undefined) {
      let parsed;
      try {
        parsed = JSON.parse(req.metadata);
      } catch (e) {
        throw new Error("metadata must be a JSON object string");
      }
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error("metadata must be a JSON object string");
      req.metadata = parsed;
    }
    const normalized = normalizeRequest(req, ctx.model);
    if (hasInputReferenceFile && normalized.image_urls && normalized.image_urls.length) throw new Error("provide reference images through one field");
    return {
      kind: "submit",
      model: ctx.model,
      action: hasInputReferenceFile || (normalized.image_urls && normalized.image_urls.length) ? "image_to_video" : "text_to_video",
      requestBody: normalized,
    };
  },
  render: function (ctx, task) {
    if (task.data && typeof task.data === "object" && !Array.isArray(task.data)) return task.data;
    return {};
  },
};
