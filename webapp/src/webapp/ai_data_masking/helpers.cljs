(ns webapp.ai-data-masking.helpers
  (:require [reagent.core :as r]
            [clojure.string]
            [webapp.connections.dlp-info-types :as dlp-types]))

;; Presets definitions
(def preset-definitions
  {"KEYS_AND_PASSWORDS" {:text "Keys and Passwords"
                         :values ["AUTH_TOKEN" "PASSWORD" "GENERIC_ID" "HTTP_COOKIE" "JSON_WEB_TOKEN"]}
   "CONTACT_INFORMATION" {:text "Contact Information"
                          :values ["EMAIL_ADDRESS" "PHONE_NUMBER" "PERSON_NAME" "STREET_ADDRESS"]}
   "PERSONAL_INFORMATION" {:text "Personal Information"
                           :values ["DATE_OF_BIRTH" "CREDIT_CARD_NUMBER" "MEDICAL_RECORD_NUMBER" "PASSPORT"]}})



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

(defn- format-rule [rule]
  (if (empty? (:type rule))
    (create-empty-rule)
    {:type (:type rule)
     :rule (:rule rule)
     :details (:details rule)
     :selected false
     :timestamp (:timestamp rule)}))

(defn- format-custom-rule [rule]
  (if (empty? (:name rule))
    (create-empty-custom-rule)
    {:name (:name rule)
     :regex (:regex rule)
     :score (:score rule)
     :selected false
     :timestamp (:timestamp rule)}))

;; Helper function to reverse preset name transformation (from API format back to internal format)
(defn reverse-preset-name [api-name]
  (case api-name
    "KEY-AND-PASSWORDS" "KEYS_AND_PASSWORDS"
    "CONTACT-INFORMATION" "CONTACT_INFORMATION"
    "PERSONAL-INFORMATION" "PERSONAL_INFORMATION"
    "CUSTOM-SELECTION" "fields"
    api-name)) ; fallback to original name

(defn- format-supported-entity-types [supported-entity-types]
  (if (empty? supported-entity-types)
    [(create-empty-rule)]
    (mapcat (fn [entity-type]
              (let [internal-type (reverse-preset-name (:name entity-type))]
                (map (fn [value]
                       {:type internal-type
                        :rule value
                        :details ""
                        :selected false
                        :timestamp (.now js/Date)})
                     (or (:entity_types entity-type) (:values entity-type)))))
            supported-entity-types)))

(defn- format-custom-rules [rules]
  (if (empty? rules)
    [(create-empty-custom-rule)]
    (mapv format-custom-rule rules)))

(defn create-form-state [initial-data]
  {:id (r/atom (or (:id initial-data) ""))
   :name (r/atom (or (:name initial-data) ""))
   :description (r/atom (or (:description initial-data) ""))
   :connection_ids (r/atom (or (:connection_ids initial-data) []))
   :rules (r/atom (vec (format-supported-entity-types (or (:supported_entity_types initial-data) []))))
   :custom-rules (r/atom (vec (format-custom-rules (or (:custom_entity_types initial-data) []))))
   :rules-select-state (r/atom false)
   :custom-rules-select-state (r/atom false)})

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
                (empty? (:rule rule))))
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

;; Helper function to transform preset names to API format (with hyphens)
(defn transform-preset-name [preset-name]
  (case preset-name
    "KEYS_AND_PASSWORDS" "KEY-AND-PASSWORDS"
    "CONTACT_INFORMATION" "CONTACT-INFORMATION"
    "PERSONAL_INFORMATION" "PERSONAL-INFORMATION"
    preset-name)) ; fallback to original name

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
                    ; For fields, create a single CUSTOM-SELECTION entry
                   {:name "CUSTOM-SELECTION"
                    :entity_types (mapv :rule group-rules)}
                    ; For presets, expand to their actual values
                   {:name (transform-preset-name group-key)
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
          {:value field :text field})
        dlp-types/presidio-options))
