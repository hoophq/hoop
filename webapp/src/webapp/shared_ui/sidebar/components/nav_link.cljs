(ns webapp.shared-ui.sidebar.components.nav-link
  (:require ["@radix-ui/themes" :refer [Badge]]
            [re-frame.core :as rf]
            [webapp.shared-ui.sidebar.styles :as styles]))

(defn nav-link
  "Reusable navigation link component.
   Parameters:
   - props: map with:
     - :uri - link URI
     - :icon - function that returns the icon
     - :label - link text
     - :admin-only? - whether it's admin-only
     - :admin? - whether the user is an admin
     - :current-route - current route
     - :navigate - keyword for re-frame navigation
     - :action - alternative action instead of navigation (opens command palette, etc)
     - :badge - optional badge component
     - :feature-flag - optional feature-flag keyword; when set, link is hidden unless flag is enabled
     - :free-feature? - whether the feature is available on the free license
     - :free-license? - whether the current org is on the free license
     - :upgrade-plan-route - upgrade route (defaults to :upgrade-plan)
     - :on-activate - callback after activation (e.g. close mobile sidebar)"
  [{:keys [uri icon label feature-flag free-feature? admin-only? admin? current-route free-license? navigate action badge upgrade-plan-route on-activate]
    :or {upgrade-plan-route :upgrade-plan}}]
  (let [blocked? (and free-license? (not free-feature?))
        feature-flag? (not (nil? feature-flag))
        feature-flag-blocked? (and feature-flag? (not @(rf/subscribe [:gateway->feature-flag-enabled? feature-flag])))
        active? (= uri current-route)
        base-class (str (styles/hover-side-menu-link uri current-route)
                        (:enabled styles/link-styles))
        content [:<>
                 [:div {:class "flex gap-3 items-center"}
                  [:div {:class "shrink-0"}
                   [icon {:aria-hidden "true"}]]
                  label]
                 [:div {:class "flex gap-2 items-center"}
                  (when (string? badge)
                    [:> Badge {:color "indigo" :variant "solid" :size "1"}
                     badge])
                  (when (fn? badge)
                    [badge])]]]
    (when-not (or (and admin-only? (not admin?)) feature-flag-blocked?)
      [:li
       (if action
         [:button {:on-click (fn []
                               (action)
                               (when on-activate (on-activate)))
                   :class (str base-class " cursor-pointer w-full")}
          content]
         [:a {:href uri
              :on-click (fn [e]
                          (.preventDefault e)
                          (when navigate
                            (rf/dispatch [:navigate navigate]))
                          (when on-activate (on-activate)))
              :class base-class
              :aria-current (when active? "page")}
          content])])))
