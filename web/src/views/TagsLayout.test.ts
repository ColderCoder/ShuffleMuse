import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import TagsLayout from './TagsLayout.vue'

const TagsContent = { template: '<h1>Tags content</h1>' }
const GraveyardContent = { template: '<h1>Graveyard content</h1>' }
const RouterHost = { components: { RouterView }, template: '<RouterView />' }

function createTagsRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{
      path: '/tags',
      component: TagsLayout,
      children: [
        { path: '', component: TagsContent },
        { path: 'graveyard', component: GraveyardContent },
      ],
    }],
  })
}

describe('TagsLayout', () => {
  it('switches content and exact active tab within one secondary navigation', async () => {
    const router = createTagsRouter()
    await router.push('/tags')
    await router.isReady()
    const wrapper = mount(RouterHost, { global: { plugins: [router] } })

    const nav = wrapper.get('nav[aria-label="Tags sections"]')
    const links = nav.findAll('a')
    expect(links.map(link => link.text())).toEqual(['Tags', 'Graveyard'])
    expect(links[0].classes()).toContain('tags-subnav-link--active')
    expect(links[1].classes()).not.toContain('tags-subnav-link--active')
    expect(wrapper.text()).toContain('Tags content')

    await links[1].trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.fullPath).toBe('/tags/graveyard')
    expect(links[0].classes()).not.toContain('tags-subnav-link--active')
    expect(links[1].classes()).toContain('tags-subnav-link--active')
    expect(wrapper.text()).toContain('Graveyard content')
    expect(wrapper.text()).not.toContain('Tags content')
  })
})
