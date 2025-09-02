(ns webapp.components.command-palette-constants
  (:require
   ["lucide-react" :refer [SquareCode GalleryVerticalEnd Inbox Settings]]
   [webapp.shared-ui.sidebar.constants :as sidebar-constants]))

;; Estrutura simplificada - apenas páginas diretas + busca
(def main-navigation-items
  ;; Páginas estáticas baseadas no menu lateral
  (concat
   ;; Main routes do sidebar
   (map (fn [route]
          {:id (:name route)
           :label (:label route)
           :icon (fn [] [(get sidebar-constants/icons-registry (:name route)) {:size 16}])
           :type :navigation
           :action :navigate
           :route (:navigate route)
           :keywords [(:label route) (:name route)]})
        sidebar-constants/main-routes)

   ;; Discover routes do sidebar
   (map (fn [route]
          {:id (:name route)
           :label (:label route)
           :icon (fn [] [(get sidebar-constants/icons-registry (:name route)) {:size 16}])
           :type :navigation
           :action :navigate
           :route (:navigate route)
           :keywords [(:label route) (:name route)]})
        sidebar-constants/discover-routes)

   ;; Organization routes do sidebar
   (map (fn [route]
          {:id (:name route)
           :label (:label route)
           :icon (fn [] [(get sidebar-constants/icons-registry (:name route)) {:size 16}])
           :type :navigation
           :action :navigate
           :route (:navigate route)
           :keywords [(:label route) (:name route)]})
        sidebar-constants/organization-routes)

   ;; Integrations (flatten)
   (map (fn [integration]
          {:id (:name integration)
           :label (:label integration)
           :icon (fn [] [(get sidebar-constants/icons-registry (:name integration)
                              (fn [& _] [:> Settings {:size 16}])) {:size 16}])
           :type :navigation
           :action :navigate
           :route (:navigate integration)
           :keywords [(:label integration) (:name integration) "integration"]})
        sidebar-constants/integrations-management)

   ;; Settings (flatten)
   (map (fn [setting]
          {:id (:name setting)
           :label (:label setting)
           :icon (fn [] [(get sidebar-constants/icons-registry (:name setting)
                              (fn [& _] [:> Settings {:size 16}])) {:size 16}])
           :type :navigation
           :action :navigate
           :route (:navigate setting)
           :keywords [(:label setting) (:name setting) "settings"]})
        sidebar-constants/settings-management)))

;; Ações específicas por tipo de conexão
(def connection-actions
  {:database
   [{:id "web-terminal"
     :label "Open in Web Terminal"
     :icon (fn [] [:> SquareCode {:size 16}])
     :type :navigate
     :action :navigate
     :route :editor-plugin}
    {:id "local-terminal"
     :label "Open in Local Terminal"
     :icon (fn [] [:> SquareCode {:size 16}])
     :type :external
     :action :external}
    {:id "native-client"
     :label "Open in Native Client"
     :icon (fn [] [:> SquareCode {:size 16}])
     :type :external
     :action :external}
    {:id "recent-sessions"
     :label "Recent Sessions"
     :icon (fn [] [:> GalleryVerticalEnd {:size 16}])
     :type :navigate
     :action :navigate
     :route :sessions}
    {:id "reviews"
     :label "Reviews"
     :icon (fn [] [:> Inbox {:size 16}])
     :type :navigate
     :action :navigate
     :route :reviews-plugin}
    {:id "configure"
     :label "Configure"
     :icon (fn [] [:> Settings {:size 16}])
     :type :navigate
     :action :navigate
     :route :edit-connection}]

   :custom
   [{:id "web-terminal"
     :label "Open in Web Terminal"
     :icon (fn [] [:> SquareCode {:size 16}])
     :type :navigate
     :action :navigate
     :route :editor-plugin}
    {:id "recent-sessions"
     :label "Recent Sessions"
     :icon (fn [] [:> GalleryVerticalEnd {:size 16}])
     :type :navigate
     :action :navigate
     :route :sessions}
    {:id "configure"
     :label "Configure"
     :icon (fn [] [:> Settings {:size 16}])
     :type :navigate
     :action :navigate
     :route :edit-connection}]

   :default
   [{:id "configure"
     :label "Configure"
     :icon (fn [] [:> Settings {:size 16}])
     :type :navigate
     :action :navigate
     :route :edit-connection}]})
