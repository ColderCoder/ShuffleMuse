import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import LoginView from './LoginView.vue'
import * as api from '../api'

vi.mock('../api', () => ({
  login: vi.fn(),
  logout: vi.fn(),
  getStatus: vi.fn(),
}))

describe('LoginView', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('disables submission for the Retry-After countdown', async () => {
    vi.mocked(api.login).mockRejectedValue({
      isAxiosError: true,
      response: {
        status: 429,
        data: { code: 'LOGIN_IP_BLOCKED' },
        headers: { 'retry-after': '3' },
      },
    })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/login', component: LoginView }],
    })
    await router.push('/login')
    await router.isReady()
    const wrapper = mount(LoginView, { global: { plugins: [createPinia(), router] } })
    await wrapper.get('input[type="password"]').setValue('wrong')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('3 seconds')
    await vi.advanceTimersByTimeAsync(3000)
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
  })
})
