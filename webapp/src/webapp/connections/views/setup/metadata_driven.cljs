(ns webapp.connections.views.setup.metadata-driven
  (:require
   ["@radix-ui/themes" :refer [Box Grid Heading]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.resources.constants :refer [http-proxy-subtypes]]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.connection-method :as connection-method]))

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

      :else
      [forms/input {:label label
                    :placeholder (or placeholder (str "e.g. " key))
                    :value field-value
                    :required required
                    :type (or type "password")
                    :helper-text description
                    :on-change handle-change
                    :start-adornment (when show-source-selector?
                                       [connection-method/source-selector key])}])))

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
        config-files-map (into {} (map (fn [{:keys [key value]}]
                                         [key (if (map? value) (:value value) (str value))])
                                       config-files))]
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
                                              :value (get config-files-map field-key "")
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
  (let [credentials-config (get-metadata-credentials-config connection-subtype)
        has-env-vars? (or (contains? #{"linux-vm"} connection-subtype)
                          (contains? http-proxy-subtypes connection-subtype))
        has-credentials? (seq credentials-config)
        should-show-connection-method? (or has-credentials? has-env-vars?)]
    [:form {:class "max-w-[600px]"
            :id "metadata-credentials-form"
            :on-submit (fn [e]
                         (.preventDefault e)
                         (rf/dispatch [:connection-setup/next-step :additional-config]))}
     [:> Box {:class "space-y-7"}
      (when connection-subtype
        [:<>
         (when should-show-connection-method?
           [connection-method/main connection-subtype])

         [metadata-credentials connection-subtype form-type]
         [agent-selector/main]])]]))
