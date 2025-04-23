(ns webapp.features.access-control.views.configuration
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Text Switch Button]]
   [re-frame.core :as rf]
   [reagent.core :as r]))

(defn main []
  (let [plugin-details (rf/subscribe [:plugins->plugin-details])
        enabled? (r/atom false)]

    ;; Inicializar o estado baseado no plugin
    (rf/dispatch [:plugins->get-plugin-by-name "access_control"])

    (fn []
      (let [plugin (:plugin @plugin-details)
            installed? (or (:installed? plugin) false)]

        ;; Atualizar estado do switch quando o plugin é carregado
        (when (and (not= @enabled? installed?) (not= (:status @plugin-details) :loading))
          (reset! enabled? installed?))

        [:> Box {:class "bg-white rounded-lg shadow-sm p-8 max-w-4xl"}
         [:> Heading {:size "4" :weight "medium" :class "mb-4"}
          "Access Control Settings"]

         ;; Descrição principal
         [:> Text {:size "3" :class "text-gray-500 mb-8 block"}
          "Configure access control settings for your organization."]

         ;; Toggle para habilitar/desabilitar
         [:> Box {:class "mb-8 border-b pb-8"}
          [:> Flex {:justify "between" :align "center"}
           [:> Flex {:direction "column" :gap "1"}
            [:> Heading {:size "3" :weight "medium"}
             "Enable Access Control"]
            [:> Text {:size "2" :class "text-gray-500"}
             "When activated, users are not allowed to access connections by default unless permission is given for each one."]]

           [:> Switch {:checked @enabled?
                       :disabled (= (:status @plugin-details) :loading)
                       :onCheckedChange (fn [checked]
                                          (reset! enabled? checked)
                                          (if checked
                                            (rf/dispatch [:access-control/activate])
                                            (rf/dispatch [:dialog->open
                                                          {:title "Disable Access Control"
                                                           :text "Are you sure you want to disable access control? All users will have access to all connections."
                                                           :text-action-button "Disable"
                                                           :action-button? true
                                                           :type :danger
                                                           :on-success #(rf/dispatch [:plugins->delete-plugin "access_control"])}])))}]]]

         ;; Informação adicional
         [:> Box {:class "mb-8"}
          [:> Heading {:size "3" :weight "medium" :class "mb-2"}
           "How It Works"]
          [:> Text {:size "2" :class "text-gray-500"}
           "Access Control lets you restrict which user groups can access which connections. When enabled:"]
          [:ul {:class "list-disc list-inside mt-3 text-gray-500 text-sm space-y-1"}
           [:li "Users will only see connections they have permission to access"]
           [:li "Permissions are managed by adding user groups to connections"]
           [:li "Administrators always have access to all connections"]]]

         ;; Seção de ações
         [:> Flex {:justify "end" :gap "3" :class "border-t pt-6 mt-8"}
          [:> Button {:variant "soft"
                      :color "gray"
                      :onClick #(rf/dispatch [:navigate :access-control])}
           "Back to Groups"]]]))))
