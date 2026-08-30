import { useCallback, useEffect, useMemo, useState } from 'react'

import { Copy, KeyRound, Package, RefreshCw, Sparkles } from 'lucide-react'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { api } from '@/lib/api'

interface SelfSubscription {
  id: number
  plan_id: number
  amount_total: number
  amount_used: number
  start_time: number
  end_time: number
  status: string
  source: string
  model_scope: string
}

interface SelfSubscriptionItem {
  subscription: SelfSubscription
  plan_title?: string
  plan_subtitle?: string
  plan_price_amount?: number
  plan_currency?: string
  plan_category?: string
}

interface TokenRow {
  id: number
  name: string
  status: number
  deleted_at?: string | null
}

const PACK_KEY_NAME = '流量包专属 Key'
const QUOTA_PER_CNY = 500000 // 平台口径：¥1 = 500,000 quota（1 USD = 1 CNY）

function formatDate(unix: number): string {
  return new Date(unix * 1000).toLocaleDateString('zh-CN')
}

function daysLeft(unix: number): number {
  return Math.max(0, Math.ceil((unix - Date.now() / 1000) / 86400))
}

export function TrafficPacks({
  category = 'package',
  title = '流量包',
}: {
  category?: 'package' | 'subscription'
  title?: string
}) {
  const [items, setItems] = useState<SelfSubscriptionItem[]>([])
  const [loading, setLoading] = useState(true)
  const [tokens, setTokens] = useState<TokenRow[]>([])
  const [revealedKey, setRevealedKey] = useState<string>('')
  const [keyBusy, setKeyBusy] = useState(false)
  const [copied, setCopied] = useState<string>('')

  const loadSubscriptions = useCallback(async () => {
    const res = await api.get('/api/subscription/self')
    const data = res.data?.data ?? {}
    const list: SelfSubscriptionItem[] = (data.subscriptions ?? []).filter(
      (it: SelfSubscriptionItem) =>
        (it.plan_category ?? 'package') === category
    )
    setItems(list)
  }, [category])

  const loadTokens = useCallback(async () => {
    const res = await api.get('/api/token/?p=1&size=100')
    const data = res.data?.data ?? {}
    const rows: TokenRow[] = data.items ?? data.records ?? []
    setTokens(rows.filter((row) => row.name === PACK_KEY_NAME && row.status === 1))
  }, [])

  useEffect(() => {
    setLoading(true)
    Promise.all([loadSubscriptions(), loadTokens()])
      .catch(() => toast.error('加载流量包数据失败'))
      .finally(() => setLoading(false))
  }, [loadSubscriptions, loadTokens])

  const baseUrl = `${window.location.origin}/v1`

  const copy = useCallback(async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(label)
      toast.success(`${label}已复制`)
      setTimeout(() => setCopied(''), 1500)
    } catch {
      toast.error('复制失败，请手动选择复制')
    }
  }, [])

  const packKeyToken = tokens.length > 0 ? tokens[0] : null

  const revealPackKey = useCallback(async () => {
    if (!packKeyToken) return ''
    const res = await api.post(`/api/token/${packKeyToken.id}/key`)
    return res.data?.data?.key ?? ''
  }, [packKeyToken])

  const handleGenerateKey = useCallback(
    async (item: SelfSubscriptionItem) => {
      if (!packKeyToken) {
        setKeyBusy(true)
        try {
          await api.post('/api/token/', {
            name: PACK_KEY_NAME,
            remain_quota: 500000,
            unlimited_quota: true,
            expired_time: item.subscription.end_time,
            model_limits_enabled: true,
            model_limits: item.subscription.model_scope || '',
            allow_ips: '',
            group: '',
          })
          await loadTokens()
          toast.success('专属 API Key 已生成')
        } catch {
          toast.error('生成失败，请稍后重试')
        } finally {
          setKeyBusy(false)
        }
        return
      }
      setKeyBusy(true)
      try {
        const key = await revealPackKey()
        setRevealedKey(key)
        toast.success('已取回专属 Key')
      } catch {
        toast.error('取回失败，请稍后重试')
      } finally {
        setKeyBusy(false)
      }
    },
    [packKeyToken, loadTokens, revealPackKey]
  )

  const handleResetKey = useCallback(async () => {
    if (!packKeyToken) return
    setKeyBusy(true)
    try {
      await api.delete(`/api/token/${packKeyToken.id}/`)
      setTokens([])
      setRevealedKey('')
      const res = await api.post('/api/token/', {
        name: PACK_KEY_NAME,
        remain_quota: 500000,
        unlimited_quota: true,
        expired_time: items[0]?.subscription.end_time ?? -1,
        model_limits_enabled: true,
        model_limits: items[0]?.subscription.model_scope || '',
        allow_ips: '',
        group: '',
      })
      if (res.data?.success === false) {
        throw new Error(res.data?.message || 'reset failed')
      }
      await loadTokens()
      toast.success('专属 API Key 已重置')
    } catch {
      toast.error('重置失败，请稍后重试')
    } finally {
      setKeyBusy(false)
    }
  }, [packKeyToken, items, loadTokens])

  const pythonSnippet = useMemo(() => {
    const key = revealedKey || (packKeyToken ? 'sk-****（点击「查看 Key」显示）' : '点击下方「生成专属 API Key」')
    return `from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}",
    api_key="${key}",
)

response = client.chat.completions.create(
    model="deepseek-v4-pro",
    messages=[{"role": "user", "content": "你好，请介绍一下你自己。"}]
)`
  }, [baseUrl, revealedKey, packKeyToken])

  const curlSnippet = useMemo(() => {
    const key = revealedKey || (packKeyToken ? 'sk-****' : '<生成专属 API Key>')
    return `curl ${baseUrl}/chat/completions \\
  -H "Authorization: Bearer ${key}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}'`
  }, [baseUrl, revealedKey, packKeyToken])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{title}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-4 pb-6'>
          {loading ? (
            <Card>
              <CardContent className='py-10 text-center text-sm text-muted-foreground'>
                加载中…
              </CardContent>
            </Card>
          ) : items.length === 0 ? (
            <Card>
              <CardContent className='py-10 text-center'>
                <Package className='mx-auto mb-3 h-10 w-10 text-muted-foreground' />
                <p className='text-sm text-muted-foreground'>
                  {category === 'package'
                    ? '暂无可用流量包。兑换流量包后，可在这里查看剩余额度与有效期。'
                    : '暂无可用套餐。购买套餐后，可在这里查看剩余额度与有效期。'}
                </p>
                <Button
                  className='mt-4'
                  variant='outline'
                  onClick={() => {
                    window.location.href = '/landing.html'
                  }}
                >
                  <Sparkles className='mr-1 h-4 w-4' />
                  去兑换流量包
                </Button>
              </CardContent>
            </Card>
          ) : (
            items.map((item) => {
              const sub = item.subscription
              const total = Number(sub.amount_total)
              const used = Number(sub.amount_used)
              const remain = total - used
              const pct = total > 0 ? Math.min(100, (used / total) * 100) : 0
              const pctText = pct < 1 ? pct.toFixed(2) : pct.toFixed(1)
              // 包内计价：按购买价把 quota 折算为人民币，已用+剩余恒等于购买金额
              const perCny =
                item.plan_price_amount != null && total > 0
                  ? item.plan_price_amount / total
                  : 1 / QUOTA_PER_CNY
              const toCny = (q: number) => `¥${(q * perCny).toFixed(2)}`
              const models = (sub.model_scope || '')
                .split(',')
                .map((m) => m.trim())
                .filter(Boolean)
              return (
                <Card key={sub.id}>
                  <CardHeader className='pb-2'>
                    <div className='flex items-center gap-3'>
                      <div className='flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10'>
                        <Package className='h-5 w-5 text-primary' />
                      </div>
                      <div>
                        <CardTitle className='text-base'>
                          {item.plan_title ?? `套餐 #${sub.plan_id}`}
                        </CardTitle>
                        <CardDescription className='text-xs'>
                          {item.plan_subtitle ?? sub.source}
                        </CardDescription>
                      </div>
                      <Badge variant='outline' className='ml-2'>
                        {sub.status === 'active' ? '使用中' : sub.status}
                      </Badge>
                    </div>
                  </CardHeader>
                  <CardContent className='flex flex-col gap-5'>
                    <div className='grid grid-cols-1 gap-3 sm:grid-cols-3'>
                      <div>
                        <p className='text-xs text-muted-foreground'>剩余有效期</p>
                        <p className='text-sm font-medium'>剩余 {daysLeft(sub.end_time)} 天</p>
                      </div>
                      <div>
                        <p className='text-xs text-muted-foreground'>有效期</p>
                        <p className='text-sm font-medium'>
                          {formatDate(sub.start_time)} ~ {formatDate(sub.end_time)}
                        </p>
                      </div>
                      <div>
                        <p className='text-xs text-muted-foreground'>支持模型</p>
                        <div className='mt-1 flex flex-wrap gap-1'>
                          {models.length > 0 ? (
                            models.map((m) => (
                              <Badge key={m} variant='secondary' className='text-xs'>
                                {m}
                              </Badge>
                            ))
                          ) : (
                            <span className='text-sm'>全部模型</span>
                          )}
                        </div>
                      </div>
                    </div>

                    <div>
                      <p className='mb-2 text-sm font-medium'>用量概览</p>
                      <Progress value={pct} className='h-2' />
                      <div className='mt-2 flex items-center justify-between text-xs text-muted-foreground'>
                        <span>
                          已使用 {toCny(used)} / 剩余 {toCny(remain)} / 总额度 {toCny(total)}
                        </span>
                        <span>{pctText}%</span>
                      </div>
                    </div>
                  </CardContent>
                </Card>
              )
            })
          )}

          {items.length > 0 && (
            <Card>
              <CardHeader className='pb-2'>
                <CardTitle className='flex items-center gap-2 text-base'>
                  <KeyRound className='h-4 w-4' />
                  配置
                </CardTitle>
                <CardDescription className='text-xs'>
                  调用 API 时使用以下专属 Key 与基础地址
                </CardDescription>
              </CardHeader>
              <CardContent className='flex flex-col gap-4'>
                <div>
                  <p className='mb-1 text-sm font-medium'>专属 API Key</p>
                  <p className='mb-2 text-xs text-muted-foreground'>
                    此 Key 仅当前流量包额度可用，且仅支持本流量包包含的模型，与「API 密钥」页的
                    Key 相互独立。
                  </p>
                  <div className='flex flex-wrap items-center gap-2'>
                    <code className='min-w-0 flex-1 truncate rounded-md border bg-muted/40 px-3 py-2 font-mono text-sm'>
                      {revealedKey || (packKeyToken ? 'sk-******（已生成，点击「查看 Key」显示）' : '尚未生成')}
                    </code>
                    {revealedKey && (
                      <Button
                        size='sm'
                        variant='outline'
                        onClick={() => copy(revealedKey, '专属 Key')}
                      >
                        <Copy className='mr-1 h-4 w-4' />
                        {copied === '专属 Key' ? '已复制' : '复制'}
                      </Button>
                    )}
                    <Button
                      size='sm'
                      disabled={keyBusy}
                      onClick={() => handleGenerateKey(items[0])}
                    >
                      {packKeyToken ? '查看 Key' : '生成专属 API Key'}
                    </Button>
                    {packKeyToken && (
                      <Button
                        size='sm'
                        variant='outline'
                        disabled={keyBusy}
                        onClick={handleResetKey}
                      >
                        <RefreshCw className='mr-1 h-4 w-4' />
                        重置
                      </Button>
                    )}
                  </div>
                </div>
                <div>
                  <p className='mb-1 text-sm font-medium'>Base URL</p>
                  <div className='flex items-center gap-2'>
                    <code className='min-w-0 flex-1 truncate rounded-md border bg-muted/40 px-3 py-2 font-mono text-sm'>
                      {baseUrl}
                    </code>
                    <Button size='sm' variant='outline' onClick={() => copy(baseUrl, 'Base URL')}>
                      <Copy className='mr-1 h-4 w-4' />
                      {copied === 'Base URL' ? '已复制' : '复制'}
                    </Button>
                  </div>
                </div>
              </CardContent>
            </Card>
          )}

          {items.length > 0 && (
            <Card>
              <CardHeader className='pb-2'>
                <CardTitle className='text-base'>快速开始</CardTitle>
              </CardHeader>
              <CardContent>
                <Tabs defaultValue='python'>
                  <TabsList>
                    <TabsTrigger value='python'>Python</TabsTrigger>
                    <TabsTrigger value='curl'>cURL</TabsTrigger>
                  </TabsList>
                  <TabsContent value='python'>
                    <pre className='overflow-x-auto rounded-md bg-muted/40 p-4 font-mono text-xs leading-relaxed'>
                      {pythonSnippet}
                    </pre>
                  </TabsContent>
                  <TabsContent value='curl'>
                    <pre className='overflow-x-auto rounded-md bg-muted/40 p-4 font-mono text-xs leading-relaxed'>
                      {curlSnippet}
                    </pre>
                  </TabsContent>
                </Tabs>
                <p className='mt-3 text-xs text-muted-foreground'>
                  模型用量优先从流量包抵扣，耗尽或调用其他模型时再按钱包余额计费。
                </p>
              </CardContent>
            </Card>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
