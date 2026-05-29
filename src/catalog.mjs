import { groq, loadQuery, withId } from './sanity.mjs';

// Walk up to the true root of the content tree (course/method) for a given node.
export async function fetchTopParent(id) {
  const result = await groq(withId(await loadQuery('top_parent'), id));
  return result?.[0]?.top_parent ?? id;
}

// Fetch the (depth-3) hierarchy rooted at a node id.
// Returns { railcontent_id, metadata, children: [...] } or null.
export async function fetchHierarchy(rootId) {
  const result = await groq(withId(await loadQuery('hierarchy_children'), rootId));
  return result?.[0] ?? null;
}

// Depth-first collect of leaf nodes (no children). If the root itself is a
// leaf (e.g. a single lesson), returns [root].
export function collectLeaves(node, acc = []) {
  if (!node) return acc;
  const kids = node.children || [];
  if (kids.length === 0) {
    acc.push(node);
    return acc;
  }
  for (const child of kids) collectLeaves(child, acc);
  return acc;
}

// Resolve which lesson ids to download for a target.
//   whole=false : exactly what you point at (a course -> its lessons; a lesson -> just it)
//   whole=true  : walk up to the top parent first (the entire course)
export async function resolveLessonIds(targetId, { whole = false } = {}) {
  const rootId = whole ? await fetchTopParent(targetId) : targetId;
  const root = await fetchHierarchy(rootId);
  if (!root) return { rootId, lessonIds: [] };
  const lessonIds = collectLeaves(root)
    .map((n) => n.railcontent_id)
    .filter((v) => Number.isFinite(v));
  return { rootId, lessonIds };
}
