// Attachment checks before sending, with the same rules and wording as the server
// (readUploads in internal/encatch/mytickets/handlers.go), which checks again.

const MB = 1 << 20

function ext(name) {
  const i = name.lastIndexOf('.')
  return i > 0 ? name.slice(i + 1).toLowerCase() : ''
}

// fileProblem returns what's wrong with the chosen files, or "" if they can be sent.
// uploads is the session's upload limits (null: attachments are off).
export function fileProblem(files, uploads) {
  if (!files.length) return ''
  if (!uploads) return "Attachments aren't available right now. Please send your message without them."
  if (files.length > uploads.max_files) return `Please attach up to ${uploads.max_files} files.`
  if (uploads.max_total_mb > 0) {
    const total = files.reduce((n, f) => n + f.size, 0)
    if (total > uploads.max_total_mb * MB) {
      return `Attachments can be up to ${uploads.max_total_mb} MB in total. Please send fewer or smaller files.`
    }
  }
  const any = uploads.extensions.includes('*')
  for (const f of files) {
    if (f.size <= 0) return `"${f.name}" is empty.`
    if (f.size > uploads.max_mb * MB) return `"${f.name}" is larger than ${uploads.max_mb} MB.`
    if (!any && !uploads.extensions.includes(ext(f.name))) return `"${f.name}" can't be attached: that file type isn't allowed.`
  }
  return ''
}

// limitsText describes the limits, as the forms show them.
export function limitsText(uploads) {
  if (!uploads) return ''
  return `Up to ${uploads.max_files} files, ${uploads.max_mb} MB each${uploads.max_total_mb ? `, ${uploads.max_total_mb} MB in total` : ''}`
}

export function fileSize(bytes) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < MB) return `${Math.round(bytes / 1024)} KB`
  return `${(bytes / MB).toFixed(1)} MB`
}
