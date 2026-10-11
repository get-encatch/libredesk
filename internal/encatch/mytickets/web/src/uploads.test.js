import { describe, expect, it } from 'vitest'
import { fileProblem, fileSize, limitsText } from './uploads'

const MB = 1 << 20
const file = (name, size) => ({ name, size })
const limits = { max_mb: 1, max_total_mb: 0, max_files: 5, extensions: ['pdf', 'png', 'txt'], accept: '.pdf,.png,.txt' }

// The same cases as TestUploads and TestUploadTotalLimit in mytickets_test.go.
describe('fileProblem', () => {
  it('accepts allowed files and no files', () => {
    expect(fileProblem([], limits)).toBe('')
    expect(fileProblem([file('report.pdf', 100), file('Shot.PNG', MB)], limits)).toBe('')
  })
  it.each([
    ['type', [file('setup.exe', 2)], '"setup.exe" can\'t be attached: that file type isn\'t allowed.'],
    ['no extension', [file('README', 2)], '"README" can\'t be attached: that file type isn\'t allowed.'],
    ['size', [file('big.pdf', MB + 1)], '"big.pdf" is larger than 1 MB.'],
    ['count', Array.from({ length: 6 }, () => file('a.pdf', 1)), 'Please attach up to 5 files.'],
    ['empty', [file('empty.txt', 0)], '"empty.txt" is empty.'],
  ])('refuses %s', (_, files, want) => {
    expect(fileProblem(files, limits)).toBe(want)
  })
  it('checks the total', () => {
    const total = { ...limits, max_total_mb: 1, extensions: ['txt'] }
    const half = 600 * 1024
    expect(fileProblem([file('a.txt', half), file('b.txt', half)], total))
      .toBe('Attachments can be up to 1 MB in total. Please send fewer or smaller files.')
    expect(fileProblem([file('a.txt', half)], total)).toBe('')
  })
  it('allows any type with *', () => {
    expect(fileProblem([file('tool.exe', 5)], { ...limits, extensions: ['*'] })).toBe('')
  })
  it('explains when attachments are off', () => {
    expect(fileProblem([file('a.pdf', 1)], null)).toMatch(/aren't available/)
  })
})

describe('limitsText', () => {
  it('mentions the total only when there is one', () => {
    expect(limitsText(limits)).toBe('Up to 5 files, 1 MB each')
    expect(limitsText({ ...limits, max_total_mb: 50 })).toBe('Up to 5 files, 1 MB each, 50 MB in total')
  })
})

describe('fileSize', () => {
  it('formats sizes', () => {
    expect(fileSize(512)).toBe('512 B')
    expect(fileSize(20 * 1024)).toBe('20 KB')
    expect(fileSize(3.25 * MB)).toBe('3.3 MB')
  })
})
