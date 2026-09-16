"use strict";
const crypto = require("node:crypto");
const {sha256} = require("./lib");
function createStore(config, sdkConstructor) {
  const SecretId = process.env.COS_SECRET_ID;
  const SecretKey = process.env.COS_SECRET_KEY;
  if (!SecretId || !SecretKey || [SecretId, SecretKey].includes("replace-me")) throw new Error("Set COS_SECRET_ID and COS_SECRET_KEY in the local environment");
  const COS = sdkConstructor || require("cos-nodejs-sdk-v5");
  const cos = new COS({SecretId, SecretKey, SecurityToken: process.env.COS_SESSION_TOKEN, Protocol: "https:", Timeout: 120000});
  const base = {Bucket: config.bucket, Region: config.region};
  function call(method, parameters) {
    return new Promise((resolve, reject) => cos[method]({...base, ...parameters}, (error, data) => {
      if (!error) return resolve(data);
      // SDK errors may include signed request details. Never print the raw error.
      const safe = new Error(`COS ${method} failed (HTTP ${Number(error.statusCode) || "unknown"})`);
      safe.statusCode = Number(error.statusCode);
      if (error.code === "FileAlreadyExists") safe.code = error.code;
      reject(safe);
    }));
  }
  return {
    async get(key) {
      try { const r = await call("getObject", {Key: key}); return Buffer.isBuffer(r.Body) ? r.Body : Buffer.from(r.Body); }
      catch (e) { if (e.statusCode === 404) return null; throw e; }
    },
    async put(key, bytes, {immutable = false} = {}) {
      await call("putObject", {Key: key, Body: bytes, ContentLength: bytes.length,
        ContentType: key.endsWith(".json") ? "application/json" : key.endsWith(".js") ? "application/javascript" : "application/octet-stream",
        CacheControl: immutable && !key.includes("/.publish-") ? "public, max-age=31536000, immutable" : "no-store",
        Headers: immutable ? {"x-cos-forbid-overwrite": "true"} : {}});
    },
    async remove(key) { await call("deleteObject", {Key: key}); }
  };
}
async function assertConditionalCreation(store, root, warn = console.warn) {
  // Exercise the actual object-level guarantee without bucket-list/admin access.
  // Version-enabled buckets ignore forbid-overwrite and must fail this check.
  const key = `${root}/.publish-probe-${crypto.randomUUID()}`;
  const original = Buffer.from(crypto.randomUUID());
  const replacement = Buffer.from(crypto.randomUUID());
  let failure;
  try {
    await store.put(key, original, {immutable: true});
    let rejected = false;
    try { await store.put(key, replacement, {immutable: true}); }
    catch (e) {
      if (e.statusCode !== 409 || e.code !== "FileAlreadyExists") throw e;
      rejected = true;
    }
    if (!rejected) throw new Error("COS does not enforce conditional creation; publishing requires effective forbid-overwrite support");
    const bytes = await store.get(key);
    if (!bytes || !bytes.equals(original)) throw new Error("Conditional creation changed the existing probe contents");
  } catch (error) {
    failure = error;
    throw error;
  } finally {
    try {
      const bytes = await store.get(key);
      if (bytes && (bytes.equals(original) || bytes.equals(replacement))) await store.remove(key);
      else if (bytes) throw new Error("Publish probe changed externally");
    } catch {
      const message = `Could not clean up the COS publish probe; inspect ${key} before retrying.`;
      if (!failure) throw new Error(message);
      warn(`Warning: ${message}`);
    }
  }
}
async function putImmutable(store, key, bytes) {
  const existing = await store.get(key);
  if (existing) {
    if (sha256(existing) !== sha256(bytes)) throw new Error(`Immutable object already has different contents: ${key}`);
    return;
  }
  await store.put(key, bytes, {immutable: true});
  const returned = await store.get(key);
  if (!returned || sha256(returned) !== sha256(bytes)) throw new Error(`COS read-back mismatch: ${key}`);
}
module.exports = {createStore, putImmutable, assertConditionalCreation};
