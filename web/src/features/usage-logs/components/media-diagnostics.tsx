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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { CodeBlock } from '@/components/ai-elements/code-block'
import { CopyButton } from '@/components/copy-button'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { formatTimestampToDate } from '@/lib/format'

import { getMediaDiagnostics } from '../api'

export function MediaDiagnostics(props: {
  requestId?: string
  taskId?: string
}) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['media-diagnostics', props.requestId, props.taskId],
    queryFn: () => getMediaDiagnostics(props),
    enabled: Boolean(props.requestId || props.taskId),
    retry: false,
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
  const stageLabels: Record<string, string> = {
    client_request: t('Client request'),
    submit: t('Upstream submission'),
    poll_latest: t('Latest upstream poll'),
    poll_error: t('Latest upstream poll error'),
  }

  return (
    <section
      className='min-w-0 space-y-2'
      aria-label={t('Request and response diagnostics')}
    >
      <h3 className='text-xs font-semibold'>
        {t('Request and response diagnostics')}
      </h3>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Root only. Sanitized records are kept for 7 days. Media content is omitted; size limits and omissions are marked in each record.'
        )}
      </p>
      {query.isFetching ? (
        <LoadingState inline size='sm' message={t('Loading...')} />
      ) : null}
      {query.isError ? (
        <ErrorState
          className='min-h-0 p-3'
          title={t('Failed to load diagnostics')}
          onRetry={() => void query.refetch()}
        />
      ) : null}
      {query.isSuccess && query.data.length === 0 ? (
        <EmptyState
          className='min-h-0 p-3'
          title={t('No diagnostic records')}
          description={t(
            'This request was not captured or its diagnostic records have expired.'
          )}
        />
      ) : null}
      {query.data?.map((record) => {
        const code = JSON.stringify(
          {
            request_id: record.request_id,
            task_id: record.task_id,
            channel_id: record.channel_id,
            captured_at: formatTimestampToDate(record.created_at, 'seconds'),
            expires_at: formatTimestampToDate(record.expires_at, 'seconds'),
            ...(JSON.parse(record.payload) as Record<string, unknown>),
          },
          null,
          2
        )
        return (
          <div key={record.id} className='min-w-0'>
            <div className='flex items-center justify-between gap-2 text-xs'>
              <span>{stageLabels[record.stage] || record.stage}</span>
              <CopyButton
                value={code}
                size='sm'
                aria-label={t('Copy diagnostic record')}
              />
            </div>
            <CodeBlock
              code={code}
              language='json'
              enableCollapse={false}
              maxExpandedLines={20}
            />
          </div>
        )
      })}
    </section>
  )
}
