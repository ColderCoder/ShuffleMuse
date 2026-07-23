import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TagPill from './TagPill.vue'

describe('TagPill', () => {
  it('activates with Enter and Space when interactive', async () => {
    const wrapper = mount(TagPill, { props: { name: 'favorite', interactive: true } })

    await wrapper.trigger('keydown', { key: 'Enter' })
    await wrapper.trigger('keydown', { key: ' ' })

    expect(wrapper.emitted('click')).toHaveLength(2)
    expect(wrapper.attributes('role')).toBe('button')
    expect(wrapper.attributes('tabindex')).toBe('0')
  })
})
