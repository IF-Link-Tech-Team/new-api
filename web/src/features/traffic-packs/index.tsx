import {
  BookOpenCheck,
  CheckCircle2,
  Clock3,
  Copy,
  KeyRound,
  Loader2,
  Package,
  RefreshCw,
  ShieldCheck,
  Sparkles,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
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
import {
  getApiKeys,
  fetchTokenKey,
  rotateEntitlementToken,
} from '@/features/keys/api'
import type { ApiKey } from '@/features/keys/types'
import {
  getPublicPlans,
  getSelfSubscriptionFull,
  getSelfSubscriptionOrders,
} from '@/features/subscriptions/api'
import { SubscriptionPurchaseDialog } from '@/features/subscriptions/components/dialogs/subscription-purchase-dialog'
import type {
  PlanRecord,
  SubscriptionOrderStatus,
  UserSubscriptionRecord,
} from '@/features/subscriptions/types'
import { getTopupInfo } from '@/features/wallet/api'
import type { TopupInfo } from '@/features/wallet/types'
import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

function formatDate(unix: number): string {
  return new Date(unix * 1000).toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function normalizeKey(key: string): string {
  const value = key.trim()
  return value && !value.startsWith('sk-') ? `sk-${value}` : value
}

function productLabel(productType?: string): string {
  switch (productType) {
    case 'monthly_subscription':
      return '月套餐'
    case 'pack_ds_glm':
      return 'DS / GLM 性价比包'
    case 'pack_qwen':
      return 'Qwen 专项包'
    case 'pack_k3':
      return 'Kimi K3 专项包'
    default:
      return '专项流量包'
  }
}

function orderState(order: SubscriptionOrderStatus): {
  label: string
  tone: string
} {
  if (order.status !== 'success') {
    return order.status === 'pending'
      ? { label: '等待支付', tone: 'text-amber-600' }
      : { label: '订单未完成', tone: 'text-muted-foreground' }
  }
  if (order.provisioning_status === 'provisioned') {
    return { label: '权益与 Key 已到账', tone: 'text-emerald-600' }
  }
  if (order.provisioning_status === 'failed') {
    return { label: '到账异常，等待恢复', tone: 'text-destructive' }
  }
  return { label: '已支付，正在配置 Key', tone: 'text-amber-600' }
}

export function TrafficPacks({
  category = 'package',
  title = '流量包',
}: {
  category?: 'package' | 'subscription'
  title?: string
}) {
  const [plans, setPlans] = useState<PlanRecord[]>([])
  const [subscriptions, setSubscriptions] = useState<UserSubscriptionRecord[]>(
    []
  )
  const [tokens, setTokens] = useState<ApiKey[]>([])
  const [orders, setOrders] = useState<SubscriptionOrderStatus[]>([])
  const [topupInfo, setTopupInfo] = useState<TopupInfo | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [selectedPlan, setSelectedPlan] = useState<PlanRecord | null>(null)
  const [purchaseOpen, setPurchaseOpen] = useState(false)
  const [revealedKeys, setRevealedKeys] = useState<Record<number, string>>({})
  const [keyBusy, setKeyBusy] = useState<number | null>(null)

  const loadData = useCallback(async () => {
    setError('')
    try {
      const [planRes, selfRes, tokenRes, orderRes, topupRes] =
        await Promise.all([
          getPublicPlans(),
          getSelfSubscriptionFull(),
          getApiKeys({ p: 1, size: 100 }),
          getSelfSubscriptionOrders(),
          getTopupInfo(),
        ])
      setPlans(
        (planRes.data || []).filter(
          (item) =>
            item.plan.category === category && Boolean(item.plan.product_type)
        )
      )
      setSubscriptions(
        (selfRes.data?.all_subscriptions || []).filter(
          (item) => item.plan_category === category
        )
      )
      setTokens((tokenRes.data?.items || []) as ApiKey[])
      setOrders(orderRes.data || [])
      setTopupInfo(topupRes.data || null)
    } catch {
      setError('商品与权益数据加载失败，请稍后重试。')
    } finally {
      setLoading(false)
    }
  }, [category])

  useEffect(() => {
    void loadData()
  }, [loadData])

  const returnedTradeNo = useMemo(() => {
    const params = new URLSearchParams(window.location.search)
    return (
      params.get('alipay_return') ||
      sessionStorage.getItem('cilai_pending_subscription_order') ||
      ''
    )
  }, [])

  useEffect(() => {
    if (!returnedTradeNo) return
    let stopped = false
    let attempt = 0
    const poll = async () => {
      attempt += 1
      try {
        const response = await getSelfSubscriptionOrders(returnedTradeNo)
        const order = response.data?.[0]
        if (order && !stopped) {
          setOrders((current) => [
            order,
            ...current.filter((item) => item.trade_no !== order.trade_no),
          ])
          if (
            order.status === 'success' &&
            order.provisioning_status === 'provisioned'
          ) {
            sessionStorage.removeItem('cilai_pending_subscription_order')
            window.history.replaceState({}, '', window.location.pathname)
            await loadData()
            toast.success('支付成功，权益与专属 Key 已到账')
            return
          }
        }
      } catch {
        // A later poll can recover from a transient request failure.
      }
      if (!stopped && attempt < 20) window.setTimeout(poll, 1500)
    }
    void poll()
    return () => {
      stopped = true
    }
  }, [loadData, returnedTradeNo])

  const tokenBySubscription = useMemo(() => {
    const map = new Map<number, ApiKey>()
    for (const token of tokens) {
      if (token.user_subscription_id) {
        map.set(token.user_subscription_id, token)
      }
    }
    return map
  }, [tokens])

  const activeMonthly = useMemo(
    () =>
      subscriptions.find(
        (item) =>
          item.subscription.status === 'active' &&
          item.subscription.end_time > Date.now() / 1000 &&
          item.subscription.product_type === 'monthly_subscription'
      ),
    [subscriptions]
  )

  const unresolvedOrders = orders.filter(
    (order) =>
      order.status === 'pending' ||
      (order.status === 'success' &&
        order.provisioning_status !== 'provisioned')
  )

  const openPurchase = (plan: PlanRecord) => {
    setSelectedPlan(plan)
    setPurchaseOpen(true)
  }

  const revealKey = async (tokenId: number) => {
    setKeyBusy(tokenId)
    try {
      const response = await fetchTokenKey(tokenId)
      const key = response.data?.key
      if (!response.success || !key) throw new Error()
      setRevealedKeys((current) => ({
        ...current,
        [tokenId]: normalizeKey(key),
      }))
    } catch {
      toast.error('读取 Key 失败，请稍后重试')
    } finally {
      setKeyBusy(null)
    }
  }

  const rotateKey = async (tokenId: number) => {
    if (!window.confirm('轮换后旧 Key 会立即失效，是否继续？')) return
    setKeyBusy(tokenId)
    try {
      const response = await rotateEntitlementToken(tokenId)
      const key = response.data?.key
      if (!response.success || !key) throw new Error()
      setRevealedKeys((current) => ({
        ...current,
        [tokenId]: normalizeKey(key),
      }))
      toast.success('专属 Key 已安全轮换')
    } catch {
      toast.error('Key 轮换失败，请稍后重试')
    } finally {
      setKeyBusy(null)
    }
  }

  const copy = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      toast.success('Key 已复制')
    } catch {
      toast.error('复制失败，请手动选择')
    }
  }

  const selectedAmountDue = useMemo(() => {
    if (!selectedPlan || category !== 'subscription' || !activeMonthly) {
      return undefined
    }
    return Math.max(
      0,
      selectedPlan.plan.price_amount -
        Number(activeMonthly.subscription.price_amount_snapshot || 0)
    )
  }, [activeMonthly, category, selectedPlan])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{title}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-7xl flex-col gap-5 pb-8'>
          <Card>
            <CardContent className='flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between sm:p-5'>
              <div>
                <p className='font-medium'>
                  {category === 'subscription'
                    ? '月套餐固定 30 天，同周期只保留一个实例与一个 Key'
                    : '专项包可重复购买，每次购买生成独立额度池与独立 Key'}
                </p>
                <p className='text-muted-foreground mt-1 text-sm'>
                  当前 Key
                  额度不足、过期或模型不匹配时，请求会直接失败，不会跨池扣费。
                </p>
              </div>
              <Button
                variant='outline'
                onClick={() => void loadData()}
                className='whitespace-nowrap'
              >
                <RefreshCw className='mr-2 h-4 w-4' />
                刷新状态
              </Button>
            </CardContent>
          </Card>

          {returnedTradeNo && unresolvedOrders.length > 0 && (
            <Alert>
              <Loader2 className='h-4 w-4 animate-spin' />
              <AlertDescription>
                正在确认支付与 Key 配置状态。只有显示“权益与 Key
                已到账”后才算完成。
              </AlertDescription>
            </Alert>
          )}

          {error && (
            <Alert variant='destructive'>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}

          <section>
            <div className='mb-3 flex items-end justify-between gap-3'>
              <div>
                <h2 className='text-lg font-semibold'>可购买商品</h2>
                <p className='text-muted-foreground text-sm'>
                  价格、模型白名单与倍率均来自后端商品合同。
                </p>
              </div>
            </div>
            {(() => {
              if (loading) {
                return (
                  <div className='grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3'>
                    {['a', 'b', 'c'].map((key) => (
                      <Skeleton key={key} className='h-64 rounded-xl' />
                    ))}
                  </div>
                )
              }
              if (plans.length === 0) {
                return (
                  <Card>
                    <CardContent className='text-muted-foreground py-10 text-center text-sm'>
                      当前没有通过价格与成本配置校验的可售商品。
                    </CardContent>
                  </Card>
                )
              }
              return (
                <div className='grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3'>
                  {plans.map((item) => {
                    const plan = item.plan
                    const isRecommended =
                      plan.product_type === 'monthly_subscription' &&
                      Math.abs(plan.price_amount - 49.9) < 0.001
                    const isMonthly =
                      plan.product_type === 'monthly_subscription'
                    const currentTier =
                      activeMonthly?.subscription.product_tier || 0
                    const tier = plan.product_tier || 0
                    const sameTier =
                      isMonthly && currentTier === tier && currentTier > 0
                    const lowerTier = isMonthly && currentTier > tier
                    const higherTier =
                      isMonthly && currentTier > 0 && tier > currentTier
                    const hasPendingFamily = unresolvedOrders.some(
                      (order) => order.product_family === plan.product_family
                    )
                    const disabled = sameTier || lowerTier || hasPendingFamily
                    const models = (plan.model_scope || '')
                      .split(',')
                      .map((model) => model.trim())
                      .filter(Boolean)
                    let purchaseLabel = '立即购买'
                    if (hasPendingFamily) purchaseLabel = '订单处理中'
                    else if (sameTier) purchaseLabel = '当前档位'
                    else if (lowerTier) purchaseLabel = '当前周期不可降档'
                    else if (higherTier) {
                      purchaseLabel = `补差升级 ¥${(
                        plan.price_amount -
                        Number(
                          activeMonthly?.subscription.price_amount_snapshot || 0
                        )
                      ).toFixed(1)}`
                    }
                    return (
                      <Card
                        key={plan.id}
                        className={cn(
                          'relative overflow-hidden',
                          isRecommended && 'border-primary shadow-sm'
                        )}
                      >
                        {isRecommended && (
                          <div className='bg-primary text-primary-foreground absolute top-0 right-0 rounded-bl-lg px-3 py-1 text-xs font-medium'>
                            主推
                          </div>
                        )}
                        <CardHeader>
                          <div className='flex items-center gap-2'>
                            {isMonthly ? (
                              <Sparkles className='text-primary h-5 w-5' />
                            ) : (
                              <Package className='text-primary h-5 w-5' />
                            )}
                            <CardTitle className='text-base'>
                              {plan.title}
                            </CardTitle>
                          </div>
                          <CardDescription>{plan.subtitle}</CardDescription>
                        </CardHeader>
                        <CardContent className='space-y-4'>
                          <div>
                            <span className='text-3xl font-semibold'>
                              ¥{plan.price_amount.toFixed(1)}
                            </span>
                            <span className='text-muted-foreground ml-1 text-sm'>
                              / 30 天
                            </span>
                          </div>
                          <div className='space-y-2 text-sm'>
                            <p className='flex items-center gap-2'>
                              <ShieldCheck className='h-4 w-4 text-emerald-600' />
                              官网价 ×
                              {Number(plan.billing_multiplier || 1).toFixed(2)}{' '}
                              扣费
                            </p>
                            <p className='flex items-center gap-2'>
                              <KeyRound className='h-4 w-4 text-blue-600' />
                              {isMonthly
                                ? '本周期沿用同一专属 Key'
                                : '购买后生成独立专属 Key'}
                            </p>
                            {plan.course_eligible && (
                              <p className='flex items-center gap-2'>
                                <BookOpenCheck className='h-4 w-4 text-amber-600' />
                                含首期基础课程权益
                              </p>
                            )}
                          </div>
                          <div className='flex flex-wrap gap-1.5'>
                            {models.map((model) => (
                              <Badge key={model} variant='secondary'>
                                {model}
                              </Badge>
                            ))}
                          </div>
                          <Button
                            className='w-full whitespace-nowrap'
                            disabled={disabled}
                            onClick={() => openPurchase(item)}
                          >
                            {purchaseLabel}
                          </Button>
                        </CardContent>
                      </Card>
                    )
                  })}
                </div>
              )
            })()}
          </section>

          {unresolvedOrders.length > 0 && (
            <section>
              <h2 className='mb-3 text-lg font-semibold'>处理中订单</h2>
              <div className='space-y-2'>
                {unresolvedOrders.map((order) => {
                  const state = orderState(order)
                  return (
                    <Card key={order.trade_no}>
                      <CardContent className='flex flex-col gap-2 p-4 sm:flex-row sm:items-center sm:justify-between'>
                        <div>
                          <p className='font-medium'>{order.plan_title}</p>
                          <p className='text-muted-foreground text-xs'>
                            {order.trade_no} ·{' '}
                            {order.order_type === 'upgrade'
                              ? '补差升级'
                              : '新购'}
                          </p>
                        </div>
                        <span className={cn('text-sm font-medium', state.tone)}>
                          {state.label}
                        </span>
                      </CardContent>
                    </Card>
                  )
                })}
              </div>
            </section>
          )}

          <section>
            <h2 className='mb-3 text-lg font-semibold'>我的权益实例</h2>
            {!loading && subscriptions.length === 0 ? (
              <Card>
                <CardContent className='text-muted-foreground py-10 text-center text-sm'>
                  暂无权益实例。购买成功并完成 Key 配置后会显示在这里。
                </CardContent>
              </Card>
            ) : (
              <div className='space-y-4'>
                {subscriptions.map((item) => {
                  const sub = item.subscription
                  const total = Number(sub.amount_total || 0)
                  const used = Number(sub.amount_used || 0)
                  const remaining = Math.max(0, total - used)
                  const usage =
                    total > 0 ? Math.min(100, (used / total) * 100) : 0
                  const faceValue = Number(
                    sub.price_amount_snapshot || item.plan_price_amount || 0
                  )
                  const remainingValue =
                    total > 0 ? faceValue * (remaining / total) : 0
                  const token = tokenBySubscription.get(sub.id)
                  const revealed = token ? revealedKeys[token.id] : ''
                  const isActive =
                    sub.status === 'active' && sub.end_time > Date.now() / 1000
                  let keyContent = (
                    <p className='text-muted-foreground text-sm'>
                      该实例已结束。
                    </p>
                  )
                  if (isActive && !token) {
                    keyContent = (
                      <p className='flex items-center gap-2 text-sm text-amber-600'>
                        <Clock3 className='h-4 w-4' />
                        正在恢复 Key 配置，请刷新状态。不要手动创建共享 Key。
                      </p>
                    )
                  } else if (token) {
                    keyContent = (
                      <div className='flex flex-col gap-2 sm:flex-row sm:items-center'>
                        <code className='bg-muted min-w-0 flex-1 truncate rounded-md px-3 py-2 text-xs'>
                          {revealed || token.key || 'sk-********'}
                        </code>
                        <div className='flex gap-2'>
                          <Button
                            size='sm'
                            variant='outline'
                            disabled={keyBusy === token.id}
                            onClick={() => void revealKey(token.id)}
                            className='whitespace-nowrap'
                          >
                            {keyBusy === token.id ? (
                              <Loader2 className='mr-1 h-4 w-4 animate-spin' />
                            ) : (
                              <KeyRound className='mr-1 h-4 w-4' />
                            )}
                            查看
                          </Button>
                          <Button
                            size='sm'
                            variant='outline'
                            disabled={!revealed}
                            onClick={() => revealed && void copy(revealed)}
                            className='whitespace-nowrap'
                          >
                            <Copy className='mr-1 h-4 w-4' />
                            复制
                          </Button>
                          <Button
                            size='sm'
                            variant='outline'
                            disabled={keyBusy === token.id}
                            onClick={() => void rotateKey(token.id)}
                            className='whitespace-nowrap'
                          >
                            <RefreshCw className='mr-1 h-4 w-4' />
                            轮换
                          </Button>
                        </div>
                      </div>
                    )
                  }
                  return (
                    <Card key={sub.id}>
                      <CardHeader>
                        <div className='flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between'>
                          <div>
                            <CardTitle className='text-base'>
                              {item.plan_title ||
                                productLabel(sub.product_type)}
                            </CardTitle>
                            <CardDescription>
                              实例 #{sub.id} · {productLabel(sub.product_type)}
                            </CardDescription>
                          </div>
                          <Badge variant={isActive ? 'default' : 'secondary'}>
                            {isActive ? '使用中' : '已结束'}
                          </Badge>
                        </div>
                      </CardHeader>
                      <CardContent className='space-y-5'>
                        <div className='grid grid-cols-1 gap-3 text-sm sm:grid-cols-3'>
                          <div>
                            <p className='text-muted-foreground text-xs'>
                              剩余权益
                            </p>
                            <p className='mt-1 font-medium'>
                              ¥{remainingValue.toFixed(2)}
                            </p>
                          </div>
                          <div>
                            <p className='text-muted-foreground text-xs'>
                              当前周期
                            </p>
                            <p className='mt-1 font-medium'>
                              {formatDate(sub.start_time)} 至{' '}
                              {formatDate(sub.end_time)}
                            </p>
                          </div>
                          <div>
                            <p className='text-muted-foreground text-xs'>
                              扣费倍率
                            </p>
                            <p className='mt-1 font-medium'>
                              官网价 ×
                              {Number(sub.billing_multiplier || 1).toFixed(2)}
                            </p>
                          </div>
                        </div>
                        <div>
                          <div className='mb-2 flex items-center justify-between text-xs'>
                            <span className='text-muted-foreground'>
                              已用 {formatQuota(used)} · 剩余{' '}
                              {formatQuota(remaining)}
                            </span>
                            <span>{usage.toFixed(1)}%</span>
                          </div>
                          <Progress value={usage} className='h-2' />
                        </div>
                        {sub.course_eligible && (
                          <p className='flex items-center gap-2 text-sm text-amber-700 dark:text-amber-400'>
                            <BookOpenCheck className='h-4 w-4' />
                            首期基础课程资格已记录，套餐到期后仍保留
                          </p>
                        )}
                        <div className='rounded-lg border p-3'>
                          <div className='mb-2 flex items-center gap-2'>
                            <KeyRound className='h-4 w-4' />
                            <span className='text-sm font-medium'>
                              实例专属 Key
                            </span>
                          </div>
                          {keyContent}
                        </div>
                      </CardContent>
                    </Card>
                  )
                })}
              </div>
            )}
          </section>

          <Alert>
            <CheckCircle2 className='h-4 w-4' />
            <AlertDescription>
              API Base URL：{window.location.origin}/v1。调用时请选择对应实例的
              Key。
            </AlertDescription>
          </Alert>
        </div>

        <SubscriptionPurchaseDialog
          open={purchaseOpen}
          onOpenChange={setPurchaseOpen}
          plan={selectedPlan}
          enableAlipay={topupInfo?.enable_alipay_topup}
          amountDueOverride={selectedAmountDue}
          purchaseMode={
            selectedAmountDue !== undefined ? 'upgrade' : 'purchase'
          }
          onPurchaseSuccess={loadData}
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
