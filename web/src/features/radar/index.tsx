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
export function CodexRadar() {
  return (
    <div className='flex min-h-0 flex-1 overflow-hidden bg-background'>
      <iframe
        src='https://codexradar.com/'
        title='Codex Radar'
        className='h-full min-h-0 w-full border-0'
        sandbox='allow-forms allow-popups allow-popups-to-escape-sandbox allow-same-origin allow-scripts'
        allow='clipboard-read; clipboard-write'
        referrerPolicy='strict-origin-when-cross-origin'
      />
    </div>
  )
}
