<script setup lang="ts">
import axios from 'axios'
import { onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const auth = useAuthStore()
const router = useRouter()
const password = ref('')
const remember = ref(false)
const error = ref('')
const submitting = ref(false)
const blockedSeconds = ref(0)
let countdown: ReturnType<typeof setInterval> | null = null

function startCountdown(seconds: number) {
  blockedSeconds.value = Math.max(1, Math.ceil(seconds))
  if (countdown) clearInterval(countdown)
  countdown = setInterval(() => {
    blockedSeconds.value = Math.max(0, blockedSeconds.value - 1)
    if (blockedSeconds.value === 0 && countdown) {
      clearInterval(countdown)
      countdown = null
      error.value = ''
    }
  }, 1000)
}

async function handleLogin() {
	if (blockedSeconds.value > 0 || submitting.value) return
  error.value = ''
  submitting.value = true
  try {
	await auth.login(password.value, remember.value)
	const redirect = typeof router.currentRoute.value.query.redirect === 'string'
	  ? router.currentRoute.value.query.redirect
	  : '/'
	await router.push(redirect)
	} catch (caught) {
    if (axios.isAxiosError(caught) && caught.response?.status === 429 && caught.response.data?.code === 'LOGIN_IP_BLOCKED') {
      const retryAfter = Number.parseInt(String(caught.response.headers['retry-after'] ?? '1'), 10)
      startCountdown(Number.isFinite(retryAfter) ? retryAfter : 1)
      error.value = `Too many failed attempts. Try again in ${blockedSeconds.value} seconds.`
    } else {
      error.value = 'Login failed. Please try again.'
    }
	} finally {
	  submitting.value = false
  }
}

onUnmounted(() => {
  if (countdown) clearInterval(countdown)
})
</script>

<template>
  <div class="view login-view">
    <div class="login-card">
      <h1 class="view-title">Login</h1>
      <form @submit.prevent="handleLogin" class="login-form">
        <div class="form-group">
          <label for="password" class="form-label">Password</label>
          <input
            id="password"
            v-model="password"
            type="password"
            class="form-input"
            placeholder="Enter password"
            required
          />
        </div>
        <div class="form-group form-group--checkbox">
          <input id="remember" v-model="remember" type="checkbox" class="form-checkbox" />
          <label for="remember" class="form-label form-label--inline">Remember me</label>
        </div>
        <p v-if="blockedSeconds > 0" class="form-error" role="alert">
          Too many failed attempts. Try again in {{ blockedSeconds }} seconds.
        </p>
        <p v-else-if="error" class="form-error" role="alert">{{ error }}</p>
        <button type="submit" class="btn btn-primary btn-block" :disabled="submitting || blockedSeconds > 0">
          {{ blockedSeconds > 0 ? `Blocked (${blockedSeconds}s)` : submitting ? 'Logging in...' : 'Login' }}
        </button>
      </form>
    </div>
  </div>
</template>
