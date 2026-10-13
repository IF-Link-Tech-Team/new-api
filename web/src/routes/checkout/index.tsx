/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { createFileRoute, redirect } from '@tanstack/react-router'
import { z } from 'zod'

import { Checkout } from '@/features/checkout'
import { useAuthStore } from '@/stores/auth-store'

const productIdSchema = z
  .string()
  .trim()
  .min(1)
  .max(120)
  .regex(/^[a-z0-9][a-z0-9_-]*$/)

const checkoutSearchSchema = z.object({
  product_id: productIdSchema.optional().catch(undefined),
})

export const Route = createFileRoute('/checkout/')({
  validateSearch: checkoutSearchSchema,
  beforeLoad: ({ location }) => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || !auth.accessToken) {
      throw redirect({
        to: '/sign-in',
        search: { redirect: location.href },
      })
    }
  },
  component: RouteComponent,
})

function RouteComponent() {
  const { product_id } = Route.useSearch()
  return <Checkout productId={product_id} />
}
