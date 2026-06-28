<script setup lang="ts">
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'

definePageMeta({ layout: 'bare' })

const { login, checkSession } = useAuth()

const mode = ref<'claim' | 'login'>('login')
const loading = ref(false)
const error = ref('')

async function onSubmit(creds: { username: string; password: string }): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    if (mode.value === 'claim') {
      const { error: e } = await apiClient.POST('/api/owner/claim', { body: creds })
      if (e) {
        // 409 means an owner already exists; surface a hint and drop to login.
        error.value = 'Could not claim — an owner already exists. Try signing in.'
        mode.value = 'login'
        return
      }
      // Claim succeeded: refresh the singleton auth state before navigating.
      await checkSession()
    }
    else {
      await login(creds.username, creds.password)
    }
    await navigateTo('/')
  }
  catch {
    error.value = 'Invalid credentials'
  }
  finally {
    loading.value = false
  }
}
</script>

<template>
  <Auth
    :mode="mode"
    :loading="loading"
    :error="error"
    @submit="onSubmit"
    @switch-mode="mode = mode === 'claim' ? 'login' : 'claim'"
  />
</template>
