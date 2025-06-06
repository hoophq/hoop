(ns webapp.shared-ui.sidebar.components.nav-link
  (:require [re-frame.core :as rf]
            [webapp.shared-ui.sidebar.styles :as styles]))

(defn nav-link
  "Componente reutilizável para links de navegação.
   Parâmetros:
   - props: mapa com:
     - :uri - URI do link
     - :icon - função que retorna o ícone
     - :label - texto do link
     - :free-feature? - se é um recurso gratuito
     - :admin-only? - se é apenas para admin
     - :admin? - se o usuário é admin
     - :current-route - rota atual
     - :free-license? - se o usuário tem licença gratuita
     - :navigate - keyword para navegação via re-frame"
  [{:keys [uri icon label free-feature? admin-only? admin? current-route free-license? navigate]}]
  (when-not (and admin-only? (not admin?))
    [:li
     [:a {:href (if (and free-license? (not free-feature?))
                  "#"
                  uri)
          :on-click (fn []
                      (when (and free-license? (not free-feature?))
                        (rf/dispatch [:navigate :upgrade-plan]))
                      (when (and navigate (not (and free-license? (not free-feature?))))
                        (rf/dispatch [:navigate navigate])))
          :class (str (styles/hover-side-menu-link uri current-route)
                      (:enabled styles/link-styles)
                      (when (and free-license? (not free-feature?))
                        " text-opacity-30"))}
      [:div {:class "flex gap-3 items-center"}
       [icon]
       label]
      (when (and free-license? (not free-feature?))
        [:div {:class styles/badge-upgrade}
         "Upgrade"])]]))
