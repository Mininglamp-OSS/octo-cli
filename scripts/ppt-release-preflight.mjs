import { randomUUID } from 'node:crypto';
import { pathToFileURL } from 'node:url';

// Only the dedicated release Bot may create probe documents. No user credential,
// ambient CLI profile, redirect, retry, or delete fallback is permitted.
export async function preflight({ env = process.env, fetch = globalThis.fetch, log = console.log } = {}) {
  let origin;
  try { origin = new URL(env.PPT_RELEASE_API_ORIGIN); } catch { /* fail closed below */ }
  const token = env.PPT_RELEASE_BOT_TOKEN;
  const space = env.PPT_RELEASE_SPACE_ID;
  if (!origin || origin.protocol !== 'https:' || origin.username || origin.password ||
      origin.pathname !== '/' || origin.search || origin.hash ||
      !/^(app_|bf_)[^\s]+$/.test(token || '') || !/^[A-Za-z0-9_-]+$/.test(space || '')) {
    throw new Error('Invalid PPT release configuration: HTTPS API origin, dedicated Bot token and Space ID are required');
  }
  const probe = randomUUID();
  log(`PPT release probe ${probe}: documents are retained for operator cleanup`);
  // Docs derives Space authority from the Bot. The configured Space is checked
  // against the receipt, never sent as an authority override.
  const headers = { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' };
  const request = async (path, method, body, key) => {
    // Catch transport/parser errors without exposing a URL, credential, or body.
    try {
      const response = await fetch(origin.origin + path, {
        method, headers: { ...headers, ...(key ? { 'Idempotency-Key': key } : {}) },
        ...(body ? { body: JSON.stringify(body) } : {}),
        redirect: 'error', signal: AbortSignal.timeout(30_000),
      });
      if (response.status !== (method === 'POST' ? 201 : 200) ||
          !/^application\/json(?:\s*;|$)/i.test(response.headers.get('content-type') || '')) {
        await response.body?.cancel();
        throw new Error('Unexpected response');
      }
      let size = 0;
      const chunks = [];
      for await (const chunk of response.body) {
        size += chunk.length;
        if (size > 8 * 1024 * 1024) throw new Error('Response too large');
        chunks.push(chunk);
      }
      return JSON.parse(Buffer.concat(chunks).toString('utf8'));
    } catch {
      throw new Error(`PPT release probe ${probe}: ${method} failed; publication blocked (no automatic retry)`);
    }
  };
  const seen = new Set();
  for (const templateId of ['blank', 'signal', 'terra', 'orbital', 'picnic']) {
    const title = `CLI release probe ${probe} ${templateId}`;
    const created = await request('/v1/bot/docs', 'POST', { docType: 'html_ppt', title, templateId }, `${probe}-${templateId}`);
    if (!created || typeof created.docId !== 'string' || !/^d_[A-Za-z0-9_-]+$/.test(created.docId) ||
        seen.has(created.docId) || created.templateId !== templateId || created.docType !== 'html_ppt' ||
        created.title !== title || created.spaceId !== space) {
      throw new Error(`PPT release probe ${probe}: ${templateId} create receipt mismatch; publication blocked`);
    }
    seen.add(created.docId);
    log(`PPT release probe ${probe}: ${templateId} created ${created.docId}`);
    const result = await request(`/v1/bot/docs/${created.docId}/ppt`, 'GET');
    const live = result?.data;
    if (live?.docId !== created.docId || !Number.isSafeInteger(live.baseRevision) || live.baseRevision < 0 ||
        live.deck?.format !== 'bento/slides' || live.deck.version !== 1 ||
        !Array.isArray(live.deck.slides) || live.deck.slides.length === 0) {
      throw new Error(`PPT release probe ${probe}: ${templateId} readback invalid; publication blocked`);
    }
  }
  log(`PPT release probe ${probe}: all five templates created and read successfully`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  preflight().catch(error => { console.error(error.message); process.exitCode = 1; });
}
