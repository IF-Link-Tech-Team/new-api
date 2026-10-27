import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  channelPriceRows,
  getAPIChannelCatalog,
  type ChannelPriceRow,
  type PriceComponents,
} from './api'
import { ChannelPricesEditor } from './editor'

export function APIChannelPrices() {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const [editing, setEditing] = useState<{
    rows: ChannelPriceRow[]
    revision: number
  } | null>(null)
  const canWrite = useAuthStore(
    (state) => (state.auth.user?.role ?? 0) >= ROLE.SUPER_ADMIN
  )
  const query = useQuery({
    queryKey: ['api-channel-prices'],
    queryFn: getAPIChannelCatalog,
  })
  const rows = query.data ? channelPriceRows(query.data) : []
  const visible = rows.filter((row) =>
    `${row.model} ${row.channel_name} ${row.upstream_model}`
      .toLowerCase()
      .includes(search.toLowerCase())
  )
  const priceText = (price?: PriceComponents) =>
    price
      ? [
          price.input,
          price.cached_input,
          price.cache_creation,
          price.output,
        ].join(' / ')
      : t('Not configured')

  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>
        {t('API Models and Pricing')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex h-full min-h-0 flex-col gap-4'>
          <Alert>
            <AlertDescription>
              {t(
                'Only enabled model and channel pairs can be sold. Prices are USD per million tokens: input / cache read / cache write / output.'
              )}
            </AlertDescription>
          </Alert>
          {query.data && !query.data.active && (
            <Alert>
              <AlertDescription>
                {t(
                  'Migration preview: these prices are not active yet. Existing production pricing remains in use.'
                )}
              </AlertDescription>
            </Alert>
          )}
          <Input
            aria-label={t('Search models or channels')}
            placeholder={t('Search models or channels')}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
          {query.isLoading && <LoadingState />}
          {query.isError && (
            <ErrorState
              onRetry={() => {
                void query.refetch()
              }}
            />
          )}
          {query.data && (
            <StaticDataTable
              className='min-h-0 flex-1 overflow-auto'
              tableClassName='min-w-[1000px]'
              data={visible}
              getRowKey={(row) => `${row.model}:${row.channel_id}`}
              emptyContent={t('No matching models or channels')}
              columns={[
                {
                  id: 'model',
                  header: t('Model'),
                  cell: (row) => (
                    <span className='font-medium break-all'>{row.model}</span>
                  ),
                },
                {
                  id: 'channel',
                  header: t('Channel'),
                  cell: (row) => (
                    <div>
                      <p>
                        {row.channel_name} · #{row.channel_id}
                      </p>
                      <p className='text-muted-foreground text-xs break-all'>
                        {row.upstream_model}
                      </p>
                    </div>
                  ),
                },
                {
                  id: 'status',
                  header: t('Status'),
                  cell: (row) => (
                    <div className='flex flex-col items-start gap-1'>
                      <Badge
                        variant={row.price?.enabled ? 'default' : 'secondary'}
                      >
                        {row.price?.enabled ? t('Enabled') : t('Disabled')}
                      </Badge>
                      {row.channel_status !== 1 && (
                        <span className='text-muted-foreground text-xs'>
                          {t('Channel disabled')}
                        </span>
                      )}
                      {!row.mapping_valid && (
                        <span className='text-destructive text-xs'>
                          {t('Mapping unavailable')}
                        </span>
                      )}
                    </div>
                  ),
                },
                {
                  id: 'price',
                  header: t('Wallet base price'),
                  cell: (row) =>
                    priceText(
                      row.price
                        ? (JSON.parse(
                            row.price.selling_price
                          ) as PriceComponents)
                        : undefined
                    ),
                },
                {
                  id: 'cost',
                  header: t('Upstream cost'),
                  cell: (row) => (
                    <div>
                      <p>{priceText(row.cost)}</p>
                      <p className='text-muted-foreground text-xs'>
                        {row.cost_verified_at > 0
                          ? t('Cost recorded on channel')
                          : t('Cost not verified')}
                      </p>
                    </div>
                  ),
                },
                {
                  id: 'order',
                  header: t('Model route position'),
                  cell: (row) =>
                    row.price?.position == null
                      ? t('Inherit channel priority')
                      : String(row.price.position),
                },
                {
                  id: 'actions',
                  header: t('Actions'),
                  cell: (row) => (
                    <Button
                      size='sm'
                      variant='outline'
                      disabled={!canWrite}
                      onClick={() => {
                        if (query.data) {
                          setEditing({
                            rows: rows.filter(
                              (item) => item.model === row.model
                            ),
                            revision: query.data.revision,
                          })
                        }
                      }}
                    >
                      {t('Edit model channels')}
                    </Button>
                  ),
                },
              ]}
            />
          )}
        </div>
        {editing && (
          <ChannelPricesEditor
            rows={editing.rows}
            revision={editing.revision}
            onClose={() => setEditing(null)}
          />
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
