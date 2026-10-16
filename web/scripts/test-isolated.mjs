/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { spawnSync } from 'node:child_process'
import { readdirSync } from 'node:fs'
import { join } from 'node:path'

// Bun 1.3's node:test bridge and React's cached DOM bindings leak across files.
// A separate process per file runs every assertion with a fresh module registry.
function testFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) return testFiles(path)
    return /\.(test|spec)\.[cm]?[jt]sx?$/.test(entry.name) ? [path] : []
  })
}

const files = testFiles('src').sort()
if (files.length === 0) throw new Error('No frontend test files found')
let failures = 0
for (const file of files) {
  const result = spawnSync(process.execPath, ['test', `./${file}`], {
    stdio: 'inherit',
    env: process.env,
  })
  if (result.error) throw result.error
  if (result.status !== 0) failures++
}
console.log(
  `Isolated test files: ${files.length - failures} passed, ${failures} failed, ${files.length} total`
)
process.exitCode = failures === 0 ? 0 : 1
