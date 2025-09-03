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

;; Função para filtrar e ajustar itens baseado em permissões e plano do usuário
(defn filter-items-by-permissions [user-data]
  (let [admin? (:admin? user-data)
        selfhosted? (= (:tenancy_type user-data) "selfhosted")
        free-license? (:free-license? user-data)
        ;; Incluir TODAS as rotas para verificação
        all-routes (concat sidebar-constants/main-routes
                           sidebar-constants/discover-routes
                           sidebar-constants/organization-routes
                           sidebar-constants/integrations-management
                           sidebar-constants/settings-management)]
    (->> main-navigation-items
         ;; APENAS filtrar por permissões básicas (admin/selfhosted)
         (filter (fn [item]
                   (let [route (first (filter #(= (:name %) (:id item)) all-routes))]
                     (and
                      ;; Verificar admin-only
                      (or (not (:admin-only? route)) admin?)
                      ;; Verificar selfhosted-only
                      (or (not (:selfhosted-only? route)) selfhosted?)))))
         ;; Ajustar rotas para upgrade quando necessário (SEM filtrar)
         (map (fn [item]
                (let [route (first (filter #(= (:name %) (:id item)) all-routes))]
                  (if (and free-license? (not (:free-feature? route)))
                    ;; Feature paga em licença gratuita - redirecionar para upgrade
                    (assoc item
                           :action :navigate
                           :route (or (:upgrade-plan-route route) :upgrade-plan)
                           :requires-upgrade? true)
                    ;; Feature normal
                    item)))))))
