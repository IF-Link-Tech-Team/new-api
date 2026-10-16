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
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { useState } from 'react'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { beforeEach, expect, it, vi } from 'vitest'

import { BillingHistoryDialog } from '@/features/wallet/components/dialogs/billing-history-dialog'
import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'

import { RechargeFormCard } from '../components/recharge-form-card'

const i18n = createInstance()

beforeEach(async () => {
  await i18n.use(initReactI18next).init({
    lng: 'zh',
    fallbackLng: false,
    nsSeparator: false,
    resources: { en, zh },
    interpolation: { escapeValue: false },
  })
})

it('translates billing history statuses and updates them when the language changes', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url.includes('/subscription/self/orders')) {
      return { data: { success: true, data: [] } }
    }
    return {
      data: {
        success: true,
        data: {
          total: 3,
          items: ['success', 'pending', 'expired'].map((status, id) => ({
            id,
            status,
            amount: 10,
            money: 10,
            trade_no: `order-${id}`,
            payment_method: 'stripe',
            create_time: 1,
          })),
        },
      },
    }
  })
  render(
    <I18nextProvider i18n={i18n}>
      <BillingHistoryDialog open onOpenChange={vi.fn()} />
    </I18nextProvider>
  )
  await screen.findByText('order-0')
  for (const label of ['成功', '待确认', '已过期']) {
    expect(screen.getByText(label)).toBeVisible()
  }
  await act(() => i18n.changeLanguage('en'))
  for (const label of ['Success', 'Pending', 'Expired']) {
    expect(screen.getByText(label)).toBeVisible()
  }
})

it('requires the configured minimum and preserves decimal custom recharge amounts', async () => {
  const user = userEvent.setup()
  const onPay = vi.fn()
  function RechargeFixture() {
    const [amount, setAmount] = useState(1)
    return (
      <I18nextProvider i18n={i18n}>
        <RechargeFormCard
          topupInfo={{
            enable_online_topup: false,
            enable_iflinkpay_topup: true,
            enable_alipay_topup: false,
            enable_stripe_topup: false,
            pay_methods: [
              { type: 'iflinkpay', name: 'IFLinkPay', min_topup: 2 },
              { type: 'alipay', name: 'Disabled Alipay' },
            ],
            min_topup: 1,
            stripe_min_topup: 1,
            amount_options: [10, 30, 50],
            discount: {},
          }}
          presetAmounts={[{ value: 10 }, { value: 30 }, { value: 50 }]}
          selectedPreset={null}
          topupAmount={amount}
          onTopupAmountChange={setAmount}
          paymentAmount={amount}
          calculating={false}
          paymentLoading={null}
          redemptionCode=''
          redeeming={false}
          onSelectPreset={vi.fn()}
          onPaymentMethodSelect={onPay}
          onRedemptionCodeChange={vi.fn()}
          onRedeem={vi.fn()}
        />
      </I18nextProvider>
    )
  }
  render(<RechargeFixture />)
  expect(screen.getByRole('button', { name: /IFLinkPay/ })).toBeDisabled()
  expect(
    screen.queryByRole('button', { name: 'Disabled Alipay' })
  ).not.toBeInTheDocument()
  const amount = screen.getByRole('spinbutton')
  await user.clear(amount)
  await user.type(amount, '2.25')
  expect(amount).toHaveValue(2.25)
  const pay = screen.getByRole('button', { name: 'IFLinkPay' })
  expect(pay).toBeEnabled()
  await user.click(pay)
  expect(onPay).toHaveBeenCalledWith({
    type: 'iflinkpay',
    name: 'IFLinkPay',
    min_topup: 2,
  })
})
