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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import type { MediaDiagnostic } from '../api'
import { TaskDetailsDialog } from '../components/dialogs/task-details-dialog'
import { MediaDiagnostics } from '../components/media-diagnostics'
import type { TaskLog } from '../types'

function renderDiagnostics(content: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{content}</QueryClientProvider>
  )
}

const task: TaskLog = {
  id: 1,
  user_id: 7,
  platform: 'sora',
  task_id: 'task_public',
  action: 'GENERATE',
  channel_id: 3,
  group: 'default',
  quota: 100,
  submit_time: 1,
  status: 'SUCCESS',
}

const record: MediaDiagnostic = {
  id: 'attempt-1',
  request_id: 'request-1',
  task_id: 'task_public',
  channel_id: 3,
  user_id: 7,
  stage: 'submit',
  created_at: 1800000000,
  expires_at: 1800604800,
  payload: JSON.stringify({
    request: { body: { model: 'mapped-model', n: 0, watermark: false } },
    response: { status: 200, body: { omitted: 'capture_size_limit' } },
  }),
}

describe('media diagnostics', () => {
  test('user and admin task details never fetch root payloads', () => {
    const get = vi.spyOn(api, 'get')
    renderDiagnostics(
      <TaskDetailsDialog
        log={task}
        isAdmin
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    )
    expect(
      screen.queryByRole('region', { name: 'Request and response diagnostics' })
    ).not.toBeInTheDocument()
    expect(get).not.toHaveBeenCalled()
  })

  test('root task details fetch on open and show request, response and omission markers', async () => {
    const get = vi
      .spyOn(api, 'get')
      .mockResolvedValue({ data: { success: true, data: [record] } })
    renderDiagnostics(
      <TaskDetailsDialog
        log={task}
        isAdmin
        isRoot
        open
        onOpenChange={() => undefined}
      />
    )
    expect(await screen.findByText('Upstream submission')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/api/task/diagnostics', {
      params: { request_id: undefined, task_id: 'task_public' },
    })
    const code = screen.getByLabelText('json')
    expect(code).toHaveTextContent('mapped-model')
    expect(code).toHaveTextContent('capture_size_limit')
    expect(code).toHaveTextContent('false')
    expect(
      screen.getByRole('button', { name: 'Copy diagnostic record' })
    ).toBeInTheDocument()
  })

  test('closed root details do not fetch diagnostic payloads', () => {
    const get = vi.spyOn(api, 'get')
    renderDiagnostics(
      <TaskDetailsDialog
        log={task}
        isAdmin
        isRoot
        open={false}
        onOpenChange={() => undefined}
      />
    )
    expect(get).not.toHaveBeenCalled()
  })

  test('pending request shows loading and an empty result explains expiry or no capture', async () => {
    let resolveRequest!: (value: unknown) => void
    vi.spyOn(api, 'get').mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveRequest = resolve
        }) as ReturnType<typeof api.get>
    )
    renderDiagnostics(<MediaDiagnostics requestId='request-empty' />)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
    resolveRequest({ data: { success: true, data: [] } })
    expect(await screen.findByText('No diagnostic records')).toBeInTheDocument()
    expect(
      screen.getByText(
        'This request was not captured or its diagnostic records have expired.'
      )
    ).toBeInTheDocument()
    expect(screen.queryByText('Loading...')).not.toBeInTheDocument()
  })

  test('business failure shows retry and a retry can load a long record', async () => {
    const user = userEvent.setup()
    const get = vi
      .spyOn(api, 'get')
      .mockResolvedValueOnce({
        data: { success: false, message: 'database unavailable' },
      })
      .mockResolvedValueOnce({
        data: {
          success: true,
          data: [
            {
              ...record,
              payload: JSON.stringify({ prompt: 'long prompt '.repeat(800) }),
            },
          ],
        },
      })
    renderDiagnostics(<MediaDiagnostics requestId='request-retry' />)
    expect(
      await screen.findByText('Failed to load diagnostics')
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('Upstream submission')).toBeInTheDocument()
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
    expect(screen.getByLabelText('json')).toHaveTextContent('long prompt')
    expect(
      screen.queryByText('Failed to load diagnostics')
    ).not.toBeInTheDocument()
  })
})
