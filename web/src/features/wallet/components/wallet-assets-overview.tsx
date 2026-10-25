/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { Link } from '@tanstack/react-router'
import {
  ArrowRight,
  Clock3,
  Copy,
  KeyRound,
  Loader2,
  Package,
  ReceiptText,
  Sparkles,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import { fetchTokenKey, getApiKeys } from '@/features/keys/api'
import { ensureApiKeyPrefix } from '@/features/keys/lib/api-key'
import type { ApiKey } from '@/features/keys/types'
import { getSelfSubscriptionFull } from '@/features/subscriptions/api'
import type { UserSubscriptionRecord } from '@/features/subscriptions/types'
import { getUserLogs } from '@/features/usage-logs/api'
import { LOG_TYPE_ENUM } from '@/features/usage-logs/constants'
import type { UsageLog } from '@/features/usage-logs/data/schema'
import { formatQuotaAsComputePoints } from '@/lib/compute-points'
import { formatQuota } from '@/lib/format'

function formatDate(unix: number): string {
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(unix * 1000)
}

function entitlementName(item: UserSubscriptionRecord): string {
  return item.plan_title || item.subscription.product_code || 'Token Plan'
}

export function WalletAssetsOverview() {
  const { t } = useTranslation()
  const [subscriptions, setSubscriptions] = useState<UserSubscriptionRecord[]>(
    []
  )
  const [tokens, setTokens] = useState<ApiKey[]>([])
  const [logs, setLogs] = useState<UsageLog[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revealedKeys, setRevealedKeys] = useState<Record<number, string>>({})
  const [keyBusy, setKeyBusy] = useState<number | null>(null)

  const loadAssets = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [subscriptionResponse, tokenResponse, logResponse] =
        await Promise.all([
          getSelfSubscriptionFull(),
          getApiKeys({ p: 1, size: 100 }),
          getUserLogs({ p: 1, page_size: 5, type: LOG_TYPE_ENUM.CONSUME }),
        ])

      if (!subscriptionResponse.success || !tokenResponse.success) {
        throw new Error(t('Asset data is temporarily unavailable.'))
      }

      setSubscriptions(subscriptionResponse.data?.all_subscriptions || [])
      setTokens((tokenResponse.data?.items || []) as ApiKey[])
      setLogs((logResponse.data?.items || []) as UsageLog[])
    } catch (assetError) {
      setError(
        assetError instanceof Error
          ? assetError.message
          : t('Asset data is temporarily unavailable.')
      )
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    void loadAssets()
  }, [loadAssets])

  const tokenBySubscription = useMemo(() => {
    const result = new Map<number, ApiKey>()
    for (const token of tokens) {
      if (token.user_subscription_id) {
        result.set(token.user_subscription_id, token)
      }
    }
    return result
  }, [tokens])

  const trafficPacks = subscriptions.filter(
    (item) => item.plan_category === 'package'
  )
  const tokenPlans = subscriptions.filter(
    (item) => item.plan_category === 'subscription'
  )

  const revealKey = async (tokenId: number) => {
    setKeyBusy(tokenId)
    try {
      const response = await fetchTokenKey(tokenId)
      const key = response.data?.key
      if (!response.success || !key) throw new Error()
      setRevealedKeys((current) => ({
        ...current,
        [tokenId]: ensureApiKeyPrefix(key),
      }))
    } catch {
      toast.error(t('Unable to reveal the API key. Please try again.'))
    } finally {
      setKeyBusy(null)
    }
  }

  const copyKey = async (key: string) => {
    try {
      await navigator.clipboard.writeText(ensureApiKeyPrefix(key))
      toast.success(t('API key copied'))
    } catch {
      toast.error(t('Copy failed. Please select the key manually.'))
    }
  }

  const renderKey = (subscriptionId: number) => {
    const token = tokenBySubscription.get(subscriptionId)
    if (!token) {
      return (
        <p className='text-muted-foreground flex items-center gap-2 text-xs'>
          <Clock3 className='h-3.5 w-3.5' />
          {t('The dedicated API key is being provisioned.')}
        </p>
      )
    }

    const revealed = revealedKeys[token.id]
    return (
      <div className='flex flex-col gap-2 sm:flex-row sm:items-center'>
        <code className='bg-muted min-w-0 flex-1 truncate rounded-md px-3 py-2 text-xs'>
          {revealed || 'sk-••••••••••••••••'}
        </code>
        <div className='flex gap-2'>
          <Button
            size='sm'
            variant='outline'
            disabled={keyBusy === token.id}
            onClick={() => void revealKey(token.id)}
          >
            {keyBusy === token.id ? (
              <Loader2 className='h-4 w-4 animate-spin' />
            ) : (
              <KeyRound className='h-4 w-4' />
            )}
            {t('Reveal')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={!revealed}
            onClick={() => revealed && void copyKey(revealed)}
          >
            <Copy className='h-4 w-4' />
            {t('Copy')}
          </Button>
        </div>
      </div>
    )
  }

  const renderEntitlement = (
    item: UserSubscriptionRecord,
    mode: 'package' | 'subscription'
  ) => {
    const subscription = item.subscription
    const total = Number(subscription.amount_total || 0)
    const used = Number(subscription.amount_used || 0)
    const remaining = Math.max(0, total - used)
    const usagePercent =
      total > 0 ? Math.min(100, Math.max(0, (used / total) * 100)) : 0
    const active =
      subscription.status === 'active' &&
      subscription.end_time > Date.now() / 1000
    const isPackage = mode === 'package'

    return (
      <Card key={subscription.id} data-card-hover='false'>
        <CardHeader className='pb-3'>
          <div className='flex items-start justify-between gap-3'>
            <div className='min-w-0'>
              <CardTitle className='truncate text-base'>
                {entitlementName(item)}
              </CardTitle>
              <CardDescription className='mt-1'>
                {t('Valid until {{date}}', {
                  date: formatDate(subscription.end_time),
                })}
              </CardDescription>
            </div>
            <Badge variant={active ? 'default' : 'secondary'}>
              {active ? t('Active') : t('Expired')}
            </Badge>
          </div>
        </CardHeader>
        <CardContent className='space-y-4'>
          <div className='grid grid-cols-2 gap-3'>
            <div>
              <p className='text-muted-foreground text-xs'>
                {isPackage
                  ? t('Remaining compute points')
                  : t('Remaining cycle benefit')}
              </p>
              <p className='mt-1 font-semibold tabular-nums'>
                {isPackage
                  ? formatQuotaAsComputePoints(remaining)
                  : formatQuota(remaining)}
              </p>
            </div>
            <div>
              <p className='text-muted-foreground text-xs'>
                {isPackage ? t('Total compute points') : t('Cycle total')}
              </p>
              <p className='mt-1 font-semibold tabular-nums'>
                {isPackage
                  ? formatQuotaAsComputePoints(total)
                  : formatQuota(total)}
              </p>
            </div>
          </div>
          <div>
            <div className='text-muted-foreground mb-2 flex justify-between text-xs'>
              <span>
                {t('{{percent}}% used', {
                  percent: usagePercent.toFixed(1),
                })}
              </span>
              <span>
                {isPackage
                  ? formatQuotaAsComputePoints(used)
                  : formatQuota(used)}
              </span>
            </div>
            <Progress value={usagePercent} className='h-2' />
          </div>
          <div className='border-t pt-3'>{renderKey(subscription.id)}</div>
        </CardContent>
      </Card>
    )
  }

  if (loading) {
    return (
      <div className='grid gap-4 lg:grid-cols-2'>
        {['package', 'subscription'].map((key) => (
          <Skeleton key={key} className='h-64 rounded-xl' />
        ))}
      </div>
    )
  }

  return (
    <div className='space-y-5'>
      {error ? (
        <Alert variant='destructive'>
          <AlertDescription className='flex flex-wrap items-center justify-between gap-3'>
            <span>{error}</span>
            <Button
              size='sm'
              variant='outline'
              onClick={() => void loadAssets()}
            >
              {t('Retry')}
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}

      <div className='grid gap-5 xl:grid-cols-2'>
        <section className='space-y-3' aria-labelledby='wallet-packs-title'>
          <div className='flex items-end justify-between gap-3'>
            <div>
              <h2
                id='wallet-packs-title'
                className='flex items-center gap-2 font-semibold'
              >
                <Package className='h-4 w-4' />
                {t('My traffic packs')}
              </h2>
              <p className='text-muted-foreground mt-1 text-sm'>
                {t('Compute points are shown separately from wallet cash.')}
              </p>
            </div>
            <Button
              size='sm'
              variant='ghost'
              render={<Link to='/traffic-packs' />}
            >
              {t('View all')}
              <ArrowRight className='h-4 w-4' />
            </Button>
          </div>
          {trafficPacks.length > 0 ? (
            <div className='space-y-3'>
              {trafficPacks.map((item) => renderEntitlement(item, 'package'))}
            </div>
          ) : (
            <Card data-card-hover='false'>
              <CardContent className='text-muted-foreground flex min-h-36 flex-col items-center justify-center gap-3 text-center text-sm'>
                <Package className='h-6 w-6' />
                <p>{t('No traffic pack entitlement yet.')}</p>
                <Button
                  size='sm'
                  variant='outline'
                  render={<Link to='/traffic-packs' />}
                >
                  {t('View IF.Learn starter pack')}
                </Button>
              </CardContent>
            </Card>
          )}
        </section>

        <section className='space-y-3' aria-labelledby='wallet-plans-title'>
          <div className='flex items-end justify-between gap-3'>
            <div>
              <h2
                id='wallet-plans-title'
                className='flex items-center gap-2 font-semibold'
              >
                <Sparkles className='h-4 w-4' />
                {t('My Token Plan')}
              </h2>
              <p className='text-muted-foreground mt-1 text-sm'>
                {t(
                  'Cycle benefits retain their original subscription meaning.'
                )}
              </p>
            </div>
            <Button
              size='sm'
              variant='ghost'
              render={<Link to='/subscriptions' />}
            >
              {t('View plans')}
              <ArrowRight className='h-4 w-4' />
            </Button>
          </div>
          {tokenPlans.length > 0 ? (
            <div className='space-y-3'>
              {tokenPlans.map((item) =>
                renderEntitlement(item, 'subscription')
              )}
            </div>
          ) : (
            <Card data-card-hover='false'>
              <CardContent className='text-muted-foreground flex min-h-36 flex-col items-center justify-center gap-3 text-center text-sm'>
                <Sparkles className='h-6 w-6' />
                <p>{t('No active Token Plan yet.')}</p>
                <Button
                  size='sm'
                  variant='outline'
                  render={<Link to='/subscriptions' />}
                >
                  {t('View Token Plans')}
                </Button>
              </CardContent>
            </Card>
          )}
        </section>
      </div>

      <section className='space-y-3' aria-labelledby='recent-usage-title'>
        <div className='flex items-center justify-between gap-3'>
          <div>
            <h2
              id='recent-usage-title'
              className='flex items-center gap-2 font-semibold'
            >
              <ReceiptText className='h-4 w-4' />
              {t('Recent API usage')}
            </h2>
            <p className='text-muted-foreground mt-1 text-sm'>
              {t(
                'Each value is converted from the quota deducted in the same log entry.'
              )}
            </p>
          </div>
          <Button
            size='sm'
            variant='outline'
            render={<Link to='/usage-logs' />}
          >
            {t('Billing details')}
          </Button>
        </div>
        <Card data-card-hover='false'>
          <CardContent className='p-0'>
            {logs.length > 0 ? (
              <div className='divide-y'>
                {logs.map((log) => (
                  <div
                    key={log.id}
                    className='grid gap-2 px-4 py-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center'
                  >
                    <div className='min-w-0'>
                      <p className='truncate text-sm font-medium'>
                        {log.model_name || t('Unknown model')}
                      </p>
                      <p className='text-muted-foreground mt-1 text-xs'>
                        {new Date(log.created_at * 1000).toLocaleString(
                          'zh-CN'
                        )}
                        {' · '}
                        {log.token_name || t('API key')}
                      </p>
                    </div>
                    <p className='font-mono text-sm font-semibold tabular-nums'>
                      {formatQuotaAsComputePoints(log.quota)}
                    </p>
                  </div>
                ))}
              </div>
            ) : (
              <div className='text-muted-foreground flex min-h-28 items-center justify-center px-4 text-center text-sm'>
                {t(
                  'No API usage yet. Your real consumption records will appear here.'
                )}
              </div>
            )}
          </CardContent>
        </Card>
      </section>
    </div>
  )
}
