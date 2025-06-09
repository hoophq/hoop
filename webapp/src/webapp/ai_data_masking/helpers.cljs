(ns webapp.ai-data-masking.helpers
  (:require [reagent.core :as r]
            [clojure.string]
            [webapp.connections.dlp-info-types :as dlp-types]))

;; Presets definitions
(def preset-definitions
  {"CONTACT_INFORMATION" {:text "Contact Information"
                          :values ["EMAIL_ADDRESS" "PHONE_NUMBER" "PERSON_NAME" "STREET_ADDRESS"]}
   "PERSONAL_INFORMATION" {:text "Personal Information"
                           :values ["DATE_OF_BIRTH" "CREDIT_CARD_NUMBER" "MEDICAL_RECORD_NUMBER" "PASSPORT"]}
   "NETWORK_IDENTIFIERS" {:text "Network Identifiers"
                          :values ["IP_ADDRESS" "MAC_ADDRESS" "MAC_ADDRESS_LOCAL" "DOMAIN_NAME" "URL"]}
   "FINANCIAL_DATA" {:text "Financial Data"
                     :values ["CREDIT_CARD_NUMBER" "CREDIT_CARD_TRACK_NUMBER" "IBAN_CODE" "SWIFT_CODE" "VAT_NUMBER"]}
   "PERSONAL_NAMES" {:text "Personal Names"
                     :values ["FIRST_NAME" "LAST_NAME" "MALE_NAME" "FEMALE_NAME" "PERSON_NAME"]}
   "MEDICAL_INFO" {:text "Medical Information"
                   :values ["MEDICAL_RECORD_NUMBER" "MEDICAL_TERM" "ICD9_CODE" "ICD10_CODE"]}
   "LOCATION_DATA" {:text "Location Data"
                    :values ["LOCATION" "LOCATION_COORDINATES" "STREET_ADDRESS" "COUNTRY_DEMOGRAPHIC"]}
   "DEVICE_IDENTIFIERS" {:text "Device Identifiers"
                         :values ["IMEI_HARDWARE_ID" "IMSI_ID" "ICCID_NUMBER" "ADVERTISING_ID"]}
   "DEMOGRAPHICS" {:text "Demographics"
                   :values ["AGE" "GENDER" "ETHNIC_GROUP" "MARITAL_STATUS"]}
   "SYSTEM_IDENTIFIERS" {:text "System Identifiers"
                         :values ["GENERIC_ID" "HTTP_COOKIE" "STORAGE_SIGNED_POLICY_DOCUMENT" "STORAGE_SIGNED_URL"]}})



;; Rule types
(def rule-types
  [{:value "presets" :text "Presets"}
   {:value "fields" :text "Fields"}
   {:value "custom" :text "Custom"}])

(defn create-empty-rule []
  {:type ""
   :rule ""
   :details ""
   :selected false
   :timestamp (.now js/Date)})

(defn create-empty-custom-rule []
  {:name ""
   :regex ""
   :score 0.8
   :selected false
   :timestamp (.now js/Date)})

(defn- format-custom-rule [rule]
  (if (empty? (:name rule))
    (create-empty-custom-rule)
    {:name (:name rule)
     :regex (:regex rule)
     :score (:score rule)
     :selected false
     :timestamp (:timestamp rule)}))

;; Helper function to convert API names back to internal format
(defn reverse-preset-name [api-name]
  (case api-name
    "CUSTOM_SELECTION" "fields"
    api-name)) ; Presets keep their underscore format (no transformation needed)

(defn- format-supported-entity-types [supported-entity-types]
  (if (empty? supported-entity-types)
    []
    (mapv (fn [entity-type]
            (let [api-name (:name entity-type)
                  entity-values (or (:entity_types entity-type) (:values entity-type))]
              (if (= api-name "CUSTOM_SELECTION")
                ; For CUSTOM_SELECTION, create a single fields rule with all values in details
                {:type "fields"
                 :rule "Custom Selection"
                 :details (vec entity-values)
                 :selected false
                 :timestamp (.now js/Date)}
                ; For presets, create a single preset rule
                (let [internal-preset-name (reverse-preset-name api-name)]
                  {:type "presets"
                   :rule internal-preset-name
                   :details ""
                   :selected false
                   :timestamp (.now js/Date)}))))
          supported-entity-types)))

;; Convert custom entity types from API to table rules
(defn- format-custom-entity-types-to-rules [custom-entity-types]
  (if (empty? custom-entity-types)
    []
    (mapv (fn [custom-type]
            {:type "custom"
             :rule (:name custom-type)
             :details (:regex custom-type)
             :selected false
             :timestamp (.now js/Date)})
          custom-entity-types)))

(defn create-form-state [initial-data]
  (let [supported-rules (format-supported-entity-types (or (:supported_entity_types initial-data) []))
        custom-rules-as-table-rules (format-custom-entity-types-to-rules (or (:custom_entity_types initial-data) []))
        all-rules (vec (concat supported-rules custom-rules-as-table-rules))]
    {:id (r/atom (or (:id initial-data) ""))
     :name (r/atom (or (:name initial-data) ""))
     :description (r/atom (or (:description initial-data) ""))
     :connection_ids (r/atom (or (:connection_ids initial-data) []))
     :rules (r/atom (if (empty? all-rules) [(create-empty-rule)] all-rules))
     :custom-rules (r/atom [(create-empty-custom-rule)]) ; Always start with empty for additional customs
     :rules-select-state (r/atom false)
     :custom-rules-select-state (r/atom false)}))

(defn create-form-handlers [state]
  {:on-rule-field-change (fn [rules-atom idx field value]
                           (swap! rules-atom assoc-in [idx field] value))

   :on-custom-rule-field-change (fn [rules-atom idx field value]
                                  (swap! rules-atom assoc-in [idx field] value))

   :on-rule-select (fn [rules-atom idx]
                     (swap! rules-atom update-in [idx :selected] not))

   :on-custom-rule-select (fn [rules-atom idx]
                            (swap! rules-atom update-in [idx :selected] not))

   :on-toggle-rules-select (fn [select-state-atom]
                             (reset! select-state-atom (not @select-state-atom)))

   :on-toggle-custom-rules-select (fn [select-state-atom]
                                    (reset! select-state-atom (not @select-state-atom)))

   :on-toggle-all-rules (fn [rules-atom]
                          (let [all-selected? (every? :selected @rules-atom)]
                            (swap! rules-atom #(mapv
                                                (fn [rule]
                                                  (assoc rule :selected (not all-selected?)))
                                                %))))

   :on-toggle-all-custom-rules (fn [rules-atom]
                                 (let [all-selected? (every? :selected @rules-atom)]
                                   (swap! rules-atom #(mapv
                                                       (fn [rule]
                                                         (assoc rule :selected (not all-selected?)))
                                                       %))))

   :on-rules-delete (fn [rules-atom]
                      (let [filtered-rules (vec (remove :selected @rules-atom))]
                        (reset! rules-atom
                                (if (empty? filtered-rules)
                                  [(create-empty-rule)]
                                  filtered-rules))))

   :on-custom-rules-delete (fn [rules-atom]
                             (let [filtered-rules (vec (remove :selected @rules-atom))]
                               (reset! rules-atom
                                       (if (empty? filtered-rules)
                                         [(create-empty-custom-rule)]
                                         filtered-rules))))

   :on-rule-add (fn [rules-atom]
                  (swap! rules-atom conj (create-empty-rule)))

   :on-custom-rule-add (fn [rules-atom]
                         (swap! rules-atom conj (create-empty-custom-rule)))

   :on-connections-change (fn [connections]
                            (let [connection-ids (mapv #(get % "value") (js->clj connections))]
                              (reset! (:connection_ids state) connection-ids)))})

(defn remove-empty-rules [rules]
  (remove (fn [rule]
            (or (empty? (:type rule))
                (if (= (:type rule) "fields")
                  ; For fields, check if details array is empty
                  (or (empty? (:details rule))
                      (not (vector? (:details rule))))
                  ; For other types, check if rule is empty
                  (empty? (:rule rule)))))
          rules))

(defn remove-empty-custom-rules [rules]
  (remove (fn [rule]
            (or (empty? (:name rule))
                (empty? (:regex rule))))
          rules))

;; Helper function to normalize entity names (UPPERCASE with underscores)
(defn normalize-entity-name [name]
  (if (or (nil? name) (empty? (clojure.string/trim name)))
    ""
    (-> name
        clojure.string/trim
        clojure.string/upper-case
        (clojure.string/replace #"\s+" "_")
        (clojure.string/replace #"[^A-Z0-9_]" ""))))



(defn prepare-supported-entity-types [rules]
  (let [clean-rules (->> rules
                         remove-empty-rules
                         (remove #(= (:type %) "custom")))
        ; Group presets by their specific name and fields together
        grouped-rules (group-by (fn [rule]
                                  (if (= (:type rule) "presets")
                                    (:rule rule) ; Group each preset by its specific name
                                    "fields"))   ; Group all fields together
                                clean-rules)]
    (->> grouped-rules
         (mapv (fn [[group-key group-rules]]
                 (if (= group-key "fields")
                   ; For fields, extract entity_types from details array
                   {:name "CUSTOM_SELECTION"
                    :entity_types (vec (mapcat :details group-rules))}
                   ; For presets, keep original names with underscores
                   {:name group-key
                    :entity_types (get-in preset-definitions [group-key :values])})))
         vec)))

(defn prepare-custom-entity-types [rules custom-rules]
  (let [; Extract custom rules from the main rules table
        custom-from-rules (->> rules
                               remove-empty-rules
                               (filter #(= (:type %) "custom"))
                               (mapv (fn [rule]
                                       {:name (normalize-entity-name (:rule rule))
                                        :regex (:details rule)
                                        :score 0.8})))
        ; Process dedicated custom rules
        processed-custom-rules (->> custom-rules
                                    remove-empty-custom-rules
                                    (mapv #(-> %
                                               (select-keys [:name :regex :score])
                                               (update :name normalize-entity-name))))]
    ; Combine both sources
    (vec (concat custom-from-rules processed-custom-rules))))

(defn prepare-payload [state]
  {:name @(:name state)
   :description @(:description state)
   :connection_ids @(:connection_ids state)
   :supported_entity_types (prepare-supported-entity-types @(:rules state))
   :custom_entity_types (prepare-custom-entity-types @(:rules state) @(:custom-rules state))})

;; Helper functions for UI
(defn get-preset-options []
  (mapv (fn [[key preset]]
          {:value key :text (:text preset)})
        preset-definitions))

(defn get-preset-values [preset-key]
  (get-in preset-definitions [preset-key :values] []))

(defn get-field-options []
  (mapv (fn [field]
          {"value" field "label" field})
        dlp-types/presidio-options))
