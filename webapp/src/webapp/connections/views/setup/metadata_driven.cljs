(ns webapp.connections.views.setup.metadata-driven
  (:require
   ["@radix-ui/themes" :refer [Box Grid Heading]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.additional-configuration :as additional-configuration]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.connection-method :as connection-method]
   [webapp.connections.views.setup.headers :as headers]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]))

(defn metadata-credential-field
  [{:keys [key label value required placeholder type description
           connection-method is-filesystem?]}]
  (let [show-source-selector? (= connection-method "secrets-manager")
        field-value (if (map? value) (:value value) (str value))
        handle-change (fn [e]
                        (let [new-value (-> e .-target .-value)]
                          (if is-filesystem?
                            (rf/dispatch [:connection-setup/update-config-file-by-key
                                          key
                                          new-value])
                            (rf/dispatch [:connection-setup/update-metadata-credentials
                                          key
                                          new-value]))))]
    (cond
      (= type "textarea")
      [forms/textarea {:label label
                       :placeholder (or placeholder (str "e.g. " key))
                       :value field-value
                       :required required
                       :helper-text description
                       :on-change handle-change}]

      show-source-selector?
      [forms/input-with-adornment {:label label
                                   :placeholder (or placeholder (str "e.g. " key))
                                   :value field-value
                                   :required required
                                   :type (or type "password")
                                   :helper-text description
                                   :on-change handle-change
                                   :show-password? true
                                   :start-adornment [connection-method/source-selector key]}]
      :else
      [forms/input {:label label
                    :placeholder (or placeholder (str "e.g. " key))
                    :value field-value
                    :required required
                    :type (or type "password")
                    :helper-text description
                    :on-change handle-change}])))

(defn metadata-credential->form-field
  "Converte credential do metadata (agora array) para formato de formulário"
  [{:keys [name type required description placeholder]}]
  {:key name
   :env-var-name name
   :label (cs/join " " (cs/split name #"_"))
   :value ""
   :required required
   :placeholder (or placeholder description)
   :original-type type
   :type (case type
           "filesystem" "textarea"
           "textarea" "textarea"
           "password")
   :description description})

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



(defn metadata-credentials [connection-subtype form-type]
  (let [configs (get-metadata-credentials-config connection-subtype)
        saved-credentials @(rf/subscribe [:connection-setup/metadata-credentials])
        raw-credentials (if (= form-type :update)
                          saved-credentials
                          @(rf/subscribe [:connection-setup/metadata-credentials]))
        full-credentials (get-in @(rf/subscribe [:connection-setup/form-data]) [:metadata-credentials] {})
        connection-method @(rf/subscribe [:connection-setup/connection-method])
        config-files @(rf/subscribe [:connection-setup/configuration-files])
        full-config-files (get-in @(rf/subscribe [:connection-setup/form-data]) [:credentials :configuration-files] [])
        config-files-map (into {} (map (fn [{:keys [key value]}]
                                         [key (if (map? value) (:value value) (str value))])
                                       config-files))
        full-config-files-map (into {} (map (fn [{:keys [key value]}]
                                              ;; Extract value from {:value :prefix} format, handling double-wrapped case
                                              [key (if (map? value)
                                                     (let [inner-value (:value value)]
                                                       (if (map? inner-value)
                                                         ;; Double-wrapped: extract the inner value
                                                         {:value (if (map? inner-value) (:value inner-value) (str inner-value))
                                                          :prefix ""}
                                                         ;; Single wrapped: use as-is
                                                         value))
                                                     ;; Plain value: wrap it
                                                     {:value (str value) :prefix ""})])
                                            full-config-files))]
    (if configs
      [:> Box {:class "space-y-5"}
       [:> Heading {:as "h3" :size "4" :weight "bold"}
        "Environment credentials"]
       [:> Grid {:columns "1" :gap "4"}
        (for [field configs
              :let [field-key (:key field)
                    env-var-name (:env-var-name field field-key)
                    is-filesystem? (= (:original-type field) "filesystem")
                    is-aws-iam-role? (= connection-method "aws-iam-role")
                    is-password? (= env-var-name "PASS")
                    should-hide? (and is-aws-iam-role? is-password?)]
              :when (not should-hide?)]
          ^{:key field-key}
          (if is-filesystem?
            [metadata-credential-field (assoc field
                                              :key field-key
                                              :value (get full-config-files-map field-key (get config-files-map field-key ""))
                                              :connection-method connection-method
                                              :is-filesystem? true)]
            (let [full-value (get full-credentials field-key)
                  credential-value (if (map? full-value)
                                     full-value
                                     (get raw-credentials field-key ""))]
              [metadata-credential-field (assoc field
                                                :key field-key
                                                :value credential-value
                                                :connection-method connection-method)])))]]
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
       [connection-method/main connection-subtype]

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
