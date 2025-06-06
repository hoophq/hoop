(ns webapp.ai-data-masking.helpers
  (:require [reagent.core :as r]
            [webapp.connections.dlp-info-types :as dlp-types]))

;; Presets definitions
(def preset-definitions
  {"KEYS_AND_PASSWORDS" {:text "Keys and Passwords"
                         :values ["AUTH_TOKEN" "PASSWORD" "GENERIC_ID" "HTTP_COOKIE" "JSON_WEB_TOKEN"]}
   "CONTACT_INFORMATION" {:text "Contact Information"
                          :values ["EMAIL_ADDRESS" "PHONE_NUMBER" "PERSON_NAME" "STREET_ADDRESS"]}
   "PERSONAL_INFORMATION" {:text "Personal Information"
                           :values ["DATE_OF_BIRTH" "CREDIT_CARD_NUMBER" "MEDICAL_RECORD_NUMBER" "PASSPORT"]}})

;; Data protection methods
(def data-protection-methods
  [{:value "content-start" :text "Content - Start"}
   {:value "content-middle" :text "Content - Middle"}
   {:value "content-end" :text "Content - End"}
   {:value "content-full" :text "Content - Full"}])

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
   :score 0.01
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

(defn- format-supported-entity-types [supported-entity-types]
  (if (empty? supported-entity-types)
    [(create-empty-rule)]
    (mapcat (fn [entity-type]
              (map (fn [value]
                     {:type (:name entity-type)
                      :rule value
                      :details ""
                      :selected false
                      :timestamp (.now js/Date)})
                   (:values entity-type)))
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
   :data_protection_method (r/atom (or (:data_protection_method initial-data) ""))
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
                            (reset! (:connection_ids state) (js->clj connections)))})

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

(defn prepare-supported-entity-types [rules]
  (->> rules
       remove-empty-rules
       (group-by :type)
       (map (fn [[type-name type-rules]]
              {:name type-name
               :values (mapv :rule type-rules)}))
       vec))

(defn prepare-custom-entity-types [custom-rules]
  (->> custom-rules
       remove-empty-custom-rules
       (mapv #(select-keys % [:name :regex :score]))
       vec))

(defn prepare-payload [state]
  {:id @(:id state)
   :name @(:name state)
   :description @(:description state)
   :connection_ids @(:connection_ids state)
   :data_protection_method @(:data_protection_method state)
   :supported_entity_types (prepare-supported-entity-types @(:rules state))
   :custom_entity_types (prepare-custom-entity-types @(:custom-rules state))})

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
