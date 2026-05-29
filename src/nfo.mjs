// Build a Kodi/Jellyfin/Emby-compatible <movie> NFO from a resolved lesson.
// Mirrors the field shape Youtarr's nfoGenerator emits (title/plot/premiered/
// runtime/studio/actor/genre/uniqueid) so the eventual media-server integration
// stays consistent.

function esc(text) {
  return String(text ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}

function dateOnly(s) {
  const m = String(s ?? '').match(/^(\d{4})-(\d{2})-(\d{2})/);
  return m ? `${m[1]}-${m[2]}-${m[3]}` : '';
}

export function buildNfo(lesson) {
  const runtimeMin = lesson.length_in_seconds
    ? Math.ceil(lesson.length_in_seconds / 60)
    : 0;
  const instructors = (lesson.instructor || []).map((i) => i?.name).filter(Boolean);
  const genres = (lesson.genre || []).map((g) => g?.name).filter(Boolean);
  const series = lesson.parent_content_data?.[0]?.title || '';
  const premiered = dateOnly(lesson.published_on);
  const uid = lesson.id ?? lesson.railcontent_id;

  const lines = ['<?xml version="1.0" encoding="UTF-8" standalone="yes"?>', '<movie>'];
  lines.push(`  <title>${esc(lesson.title)}</title>`);
  if (series) lines.push(`  <set><name>${esc(series)}</name></set>`);
  if (lesson.description) {
    const plot = String(lesson.description).replace(/<[^>]+>/g, '').trim();
    if (plot) lines.push(`  <plot>${esc(plot)}</plot>`);
  }
  if (premiered) {
    lines.push(`  <premiered>${premiered}</premiered>`);
    lines.push(`  <year>${premiered.slice(0, 4)}</year>`);
  }
  if (runtimeMin) lines.push(`  <runtime>${runtimeMin}</runtime>`);
  if (lesson.brand) lines.push(`  <studio>${esc(lesson.brand)}</studio>`);
  if (lesson.difficulty_string) lines.push(`  <tag>${esc(lesson.difficulty_string)}</tag>`);
  for (const g of genres) lines.push(`  <genre>${esc(g)}</genre>`);
  for (const name of instructors) {
    lines.push(`  <actor><name>${esc(name)}</name><role>Instructor</role></actor>`);
  }
  if (uid != null) {
    lines.push(`  <uniqueid type="musora" default="true">${esc(uid)}</uniqueid>`);
  }
  lines.push('</movie>');
  return lines.join('\n') + '\n';
}
