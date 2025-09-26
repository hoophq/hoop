(ns webapp.connections.views.setup.metadata-driven
  (:require
   ["@radix-ui/themes" :refer [Box Grid Text]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.additional-configuration :as additional-configuration]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.headers :as headers]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]))

(defn metadata-credential->form-field
  "Converte credential do metadata (agora array) para formato de formulário"
  [{:keys [name type required description placeholder]}]
  (let [form-key (cs/lower-case (cs/replace name #"[^a-zA-Z0-9]" ""))]
    {:key form-key
     :env-var-name name  ; Mantém nome original para backend
     :label name
     :value ""
     :required required
     :placeholder (or placeholder description)
     :type (case type
             "filesystem" "textarea"
             "textarea" "textarea"
             "password")
     :description description}))

(defn get-metadata-credentials-config
  "Busca credentials do metadata para uma conexão específica por subtype"
  [connection-subtype]
  (let [connections-metadata @(rf/subscribe [:connections->metadata])]
    (when connections-metadata
      (let [connection (->> (:connections connections-metadata)
                            (filter #(= (get-in % [:resourceConfiguration :subtype]) connection-subtype))
                            first)
            credentials (get-in connection [:resourceConfiguration :credentials])]

        (when (seq credentials)  ; credentials agora é array
          (let [fields (->> credentials
                            (map metadata-credential->form-field)  ; mapeia cada objeto do array
                            vec)]
            fields))))))

(defn render-field [{:keys [key label value required placeholder type description]}]
  (let [base-props {:label label
                    :placeholder (or placeholder description (str "e.g. " key))
                    :value value
                    :required required
                    :type (or type "password")
                    :on-change #(rf/dispatch [:connection-setup/update-metadata-credentials
                                              key
                                              (-> % .-target .-value)])}]
    [forms/input base-props]))

(defn metadata-credentials [connection-subtype]
  (let [configs (get-metadata-credentials-config connection-subtype)
        credentials @(rf/subscribe [:connection-setup/metadata-credentials])]
    (if configs
      [:> Box {:class "space-y-5"}
       [:> Text {:size "4" :weight "bold" :mt "6"} "Environment credentials"]
       [:> Text {:size "2" :class "text-[--gray-11] mb-4"}
        "Configure the required credentials for this connection."]

       [:> Grid {:columns "1" :gap "4"}
        (for [field configs]
          ^{:key (:key field)}
          [render-field (assoc field
                               :value (get credentials (:key field) (:value field)))])]]

      ;; Debug fallback
      [:> Box {:class "p-4 bg-red-100 text-red-800"}
       [:> Text "No configuration found for: " connection-subtype]])))

(defn credentials-step [connection-subtype _form-type]
  [:form {:class "max-w-[600px]"
          :id "metadata-credentials-form"
          :on-submit (fn [e]
                       (.preventDefault e)
                       (println "Form submitted - moving to additional-config")
                       (rf/dispatch [:connection-setup/next-step :additional-config]))}
   [:> Box {:class "space-y-7"}

    (when connection-subtype
      [:<>
       [metadata-credentials connection-subtype]
       [agent-selector/main]])]])

(defn main [form-type]
  (let [connection-subtype @(rf/subscribe [:connection-setup/connection-subtype])
        current-step @(rf/subscribe [:connection-setup/current-step])
        agent-id @(rf/subscribe [:connection-setup/agent-id])]
    [page-wrapper/main
     {:children [:> Box {:class "max-w-[600px] mx-auto p-6 space-y-7"}
                 [headers/setup-header form-type]

                 (case current-step
                   :credentials [credentials-step connection-subtype form-type]
                   :additional-config [additional-configuration/main
                                       {:selected-type connection-subtype
                                        :form-type form-type
                                        :submit-fn #(rf/dispatch [:connection-setup/submit])}]
                   nil)]

      :footer-props {:form-type form-type
                     :next-text (if (= current-step :additional-config)
                                  "Confirm"
                                  "Next")
                     :on-click (fn []
                                 (let [form (.getElementById js/document
                                                             (if (= current-step :credentials)
                                                               "metadata-credentials-form"
                                                               "additional-config-form"))]
                                   (.reportValidity form)))
                     :next-disabled? (and (= current-step :credentials)
                                          (not agent-id))
                     :on-next (fn []
                                (println "Footer Next clicked - step:" current-step "agent-id:" agent-id)
                                (let [form (.getElementById js/document
                                                            (if (= current-step :credentials)
                                                              "metadata-credentials-form"
                                                              "additional-config-form"))]
                                  (println "Form found:" (boolean form))
                                  (when form
                                    (let [is-valid (.reportValidity form)]
                                      (println "Form valid:" is-valid "Agent selected:" (boolean agent-id))
                                      (if (and is-valid agent-id)
                                        (let [event (js/Event. "submit" #js {:bubbles true :cancelable true})]
                                          (println "Dispatching form submit event")
                                          (.dispatchEvent form event))
                                        (js/console.warn "Form validation failed or agent not selected!"))))))
                     :next-hidden? (= current-step :installation)
                     :hide-footer? (= current-step :installation)}}]))
