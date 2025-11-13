(ns webapp.connections.views.setup.metadata-driven
  (:require
   ["@radix-ui/themes" :refer [Box Grid Heading]]
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
     :env-var-name name
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

        (when (seq credentials)
          (let [fields (->> credentials
                            (map metadata-credential->form-field)
                            vec)]
            fields))))))

(defn render-field [{:keys [key label value required placeholder type description]}]
  (let [base-props {:label label
                    :placeholder (or placeholder (str "e.g. " key))
                    :value value
                    :required required
                    :helper-text description
                    :type (or type "password")
                    :on-change #(rf/dispatch [:connection-setup/update-metadata-credentials
                                              key
                                              (-> % .-target .-value)])}]
    (if (= type "textarea")
      [forms/textarea base-props]
      [forms/input base-props])))

(defn metadata-credentials [connection-subtype form-type]
  (let [configs (get-metadata-credentials-config connection-subtype)
        saved-credentials @(rf/subscribe [:connection-setup/metadata-credentials])
        credentials (if (= form-type :update)
                      saved-credentials
                      @(rf/subscribe [:connection-setup/metadata-credentials]))]

    (if configs
      [:> Box {:class "space-y-5"}
       [:> Heading {:as "h3" :size "4" :weight "bold"}
        "Environment credentials"]

       [:> Grid {:columns "1" :gap "4"}
        (for [field configs]
          ^{:key (:key field)}
          [render-field (assoc field
                               :value (get credentials (:key field) ""))])]]

      nil)))

(defn credentials-step [connection-subtype form-type]
  [:form {:class "max-w-[600px]"
          :id "metadata-credentials-form"
          :on-submit (fn [e]
                       (.preventDefault e)
                       (rf/dispatch [:connection-setup/next-step :additional-config]))}
   [:> Box {:class "space-y-7"}

    (when connection-subtype
      [:<>
       [metadata-credentials connection-subtype form-type]
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
                                (let [form (.getElementById js/document
                                                            (if (= current-step :credentials)
                                                              "metadata-credentials-form"
                                                              "additional-config-form"))]
                                  (when form
                                    (let [is-valid (.reportValidity form)]
                                      (if (and is-valid agent-id)
                                        (let [event (js/Event. "submit" #js {:bubbles true :cancelable true})]
                                          (.dispatchEvent form event))
                                        (js/console.warn "Form validation failed or agent not selected!"))))))
                     :next-hidden? (= current-step :installation)
                     :hide-footer? (= current-step :installation)}}]))
