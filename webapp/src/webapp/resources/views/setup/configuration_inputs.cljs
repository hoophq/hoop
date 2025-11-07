(ns webapp.resources.views.setup.configuration-inputs
  (:require
   ["@radix-ui/themes" :refer [Box Button Grid Heading Text]]
   ["lucide-react" :refer [Plus]]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]))

(defn valid-first-char? [value]
  (boolean (re-matches #"[A-Za-z]" value)))

(defn valid-posix? [value]
  (boolean (re-matches #"[A-Za-z][A-Za-z0-9_]*" value)))

(defn parse-env-key [e role-index]
  (let [new-value (-> e .-target .-value)
        upper-value (str/upper-case new-value)
        current-value @(rf/subscribe [:resource-setup/role-env-current-key role-index])]
    (cond
      (empty? new-value)
      (rf/dispatch [:resource-setup->update-role-env-current-key role-index ""])

      ;; Handle paste operation
      (> (count new-value) 1)
      (when (valid-posix? new-value)
        (rf/dispatch [:resource-setup->update-role-env-current-key role-index upper-value]))

      (empty? current-value)
      (when (valid-first-char? new-value)
        (rf/dispatch [:resource-setup->update-role-env-current-key role-index upper-value]))

      (valid-posix? new-value)
      (rf/dispatch [:resource-setup->update-role-env-current-key role-index upper-value]))))

(defn parse-config-file-name [e role-index]
  (let [new-value (-> e .-target .-value)
        upper-value (str/upper-case new-value)
        current-value @(rf/subscribe [:resource-setup/role-config-current-name role-index])]
    (cond
      (empty? new-value)
      (rf/dispatch [:resource-setup->update-role-config-current-name role-index ""])

      ;; Handle paste operation
      (> (count new-value) 1)
      (when (valid-posix? new-value)
        (rf/dispatch [:resource-setup->update-role-config-current-name role-index upper-value]))

      (empty? current-value)
      (when (valid-first-char? new-value)
        (rf/dispatch [:resource-setup->update-role-config-current-name role-index upper-value]))

      (valid-posix? new-value)
      (rf/dispatch [:resource-setup->update-role-config-current-name role-index upper-value]))))

(defn parse-existing-env-key [e role-index idx]
  (let [new-value (-> e .-target .-value)
        upper-value (str/upper-case new-value)
        env-vars @(rf/subscribe [:resource-setup/role-env-vars role-index])
        current-value (get-in env-vars [idx :key])]
    (cond
      (empty? new-value)
      (rf/dispatch [:resource-setup->update-role-env-var role-index idx :key ""])

      ;; Handle paste operation
      (> (count new-value) 1)
      (when (valid-posix? new-value)
        (rf/dispatch [:resource-setup->update-role-env-var role-index idx :key upper-value]))

      (empty? current-value)
      (when (valid-first-char? new-value)
        (rf/dispatch [:resource-setup->update-role-env-var role-index idx :key upper-value]))

      (valid-posix? new-value)
      (rf/dispatch [:resource-setup->update-role-env-var role-index idx :key upper-value]))))

(defn parse-existing-config-file-name [e role-index idx]
  (let [new-value (-> e .-target .-value)
        upper-value (str/upper-case new-value)
        config-files @(rf/subscribe [:resource-setup/role-config-files role-index])
        current-value (get-in config-files [idx :key])]
    (cond
      (empty? new-value)
      (rf/dispatch [:resource-setup->update-role-config-file role-index idx :key ""])

      ;; Handle paste operation
      (> (count new-value) 1)
      (when (valid-posix? new-value)
        (rf/dispatch [:resource-setup->update-role-config-file role-index idx :key upper-value]))

      (empty? current-value)
      (when (valid-first-char? new-value)
        (rf/dispatch [:resource-setup->update-role-config-file role-index idx :key upper-value]))

      (valid-posix? new-value)
      (rf/dispatch [:resource-setup->update-role-config-file role-index idx :key upper-value]))))

(defn environment-variables-section [role-index {:keys [title subtitle]}]
  (let [current-key @(rf/subscribe [:resource-setup/role-env-current-key role-index])
        current-value @(rf/subscribe [:resource-setup/role-env-current-value role-index])
        env-vars @(rf/subscribe [:resource-setup/role-env-vars role-index])]
    [:> Box {:class "space-y-4"}
     [:> Heading {:size "3"} (if title title "Environment variables")]
     [:> Text {:size "2" :color "gray"}
      (if subtitle subtitle "Include environment variables to be used in your resource role.")]

     (when (seq env-vars)
       [:> Grid {:columns "2" :gap "2"}
        (for [[idx {:keys [key value]}] (map-indexed vector env-vars)]
          ^{:key (str "env-var-" idx)}
          [:<>
           [forms/input
            {:label "Key"
             :value key
             :placeholder "API_KEY"
             :on-change #(parse-existing-env-key % role-index idx)}]
           [forms/input
            {:label "Value"
             :value value
             :type "password"
             :placeholder "* * * *"
             :on-change #(rf/dispatch [:resource-setup->update-role-env-var role-index idx :value (-> % .-target .-value)])}]])])

     [:> Grid {:columns "2" :gap "2"}
      [forms/input
       {:label "Key"
        :placeholder "API_KEY"
        :value current-key
        :on-change #(parse-env-key % role-index)}]
      [forms/input
       {:label "Value"
        :placeholder "* * * *"
        :type "password"
        :value current-value
        :on-change #(rf/dispatch [:resource-setup->update-role-env-current-value role-index (-> % .-target .-value)])}]]

     [:> Button
      {:size "2"
       :variant "soft"
       :type "button"
       :on-click #(rf/dispatch [:resource-setup->add-role-env-row role-index])}
      [:> Plus {:size 16}]
      "Add key/value"]]))

(defn configuration-files-section [role-index]
  (let [config-files @(rf/subscribe [:resource-setup/role-config-files role-index])
        current-name @(rf/subscribe [:resource-setup/role-config-current-name role-index])
        current-content @(rf/subscribe [:resource-setup/role-config-current-content role-index])]
    [:> Box {:class "space-y-4"}
     [:> Heading {:size "3"} "Configuration files"]
     [:> Text {:size "2" :color "gray"}
      "Add values from your configuration file and use them as an environment variable in your resource role."]

     (when (seq config-files)
       [:> Grid {:columns "1" :gap "4"}
        (for [[idx {:keys [key value]}] (map-indexed vector config-files)]
          ^{:key (str "config-file-" idx)}
          [:<>
           [forms/input
            {:label "Name"
             :value key
             :placeholder "e.g. kubeconfig"
             :on-change #(parse-existing-config-file-name % role-index idx)}]
           [forms/textarea
            {:label "Content"
             :id (str "config-file-" idx)
             :value value
             :on-change #(rf/dispatch [:resource-setup->update-role-config-file role-index idx :value (-> % .-target .-value)])}]])])

     [:> Grid {:columns "1" :gap "4"}
      [forms/input
       {:label "Name"
        :placeholder "e.g. kubeconfig"
        :value current-name
        :on-change #(parse-config-file-name % role-index)}]
      [forms/textarea
       {:label "Content"
        :id (str "config-file-initial-" role-index)
        :placeholder "Paste your file content here"
        :value current-content
        :on-change #(rf/dispatch [:resource-setup->update-role-config-current-content
                                  role-index
                                  (-> % .-target .-value)])}]]

     [:> Button
      {:size "2"
       :variant "soft"
       :type "button"
       :on-click #(when (and (seq current-name)
                             (seq current-content))
                    (rf/dispatch [:resource-setup->add-role-config-row role-index]))}
      [:> Plus {:size 16}]
      "Add"]]))

