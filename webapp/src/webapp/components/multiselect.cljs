(ns webapp.components.multiselect
  (:require
   ["lucide-react" :refer [Award HelpCircle]]
   ["react-select" :default Select]
   ["react-select/creatable" :default CreatableSelect]
   ["@radix-ui/themes" :refer [Text Tooltip]]
   [clojure.string :as cs]
   [goog.object :as gobj]
   [reagent.core :as r]
   [webapp.components.infinite-scroll :refer [infinite-scroll]]))

(def styles
  #js {"multiValue" (fn [style]
                      (clj->js (into (js->clj style)
                                     {"padding" "4px"
                                      "borderRadius" "12px"
                                      "fontSize" "16px"
                                      "backgroundColor" "rgba(0, 0, 51, 0.06)"})))
       "multiValueLabel" (fn [style]
                           (clj->js (into (js->clj style)
                                          {"color" "#60646c"})))
       "option" (fn [style]
                  (clj->js (into (js->clj style)
                                 {"fontSize" "16px"})))
       "menuPortal" (fn [style]
                      (clj->js (into (js->clj style)
                                     {"pointerEvents" "auto"
                                      "z-index" "60"})))
       "control" (fn [style]
                   (clj->js (into (js->clj style)
                                  {"borderRadius" "9px"
                                   "fontSize" "16px"})))
       "valueContainer" (fn [style]
                          (clj->js (into (js->clj style)
                                         {"maxHeight" "200px"
                                          "overflow" "auto"})))
       "input" (fn [style]
                 (clj->js (into (js->clj style)
                                {"& > input"
                                 #js {":focus"
                                      #js {"--tw-ring-shadow" "none"}}})))})

(defn- scroll-to-bottom [ref]
  (when (some? ref)
    (let [container (.-offsetParent (.-inputRef ref))]
      (when (some? container)
        (set! (.-scrollTop container) (.-scrollHeight container))))))

(defn- form-label
  [text]
  [:> Text {:size "1" :as "label" :weight "bold" :class "text-gray-12"}
   text])

(defn main []
  (let [container-ref (r/atom nil)
        input-value (r/atom "")]
    (fn [{:keys [default-value disabled? required? on-change options label id name]}]
      [:div {:class "mb-regular text-sm"}
       [:div {:class "flex items-center gap-2 mb-1"}
        (when label
          [form-label label])]
       [:> Select
        {:value default-value
         :id id
         :name name
         :isMulti true
         :isDisabled disabled?
         :required required?
         :inputValue @input-value
         :onInputChange (fn [new-value action-meta]
                          (when (= (.-action action-meta) "input-change")
                            (reset! input-value new-value)))
         :onChange (fn [value]
                     (reset! input-value "")
                     (scroll-to-bottom @container-ref)
                     (on-change value))
         :options options
         :isClearable false
         :onFocus #(scroll-to-bottom @container-ref)
         :menuPortalTarget (.-body js/document)
         :theme (fn [theme]
                  (clj->js
                   (-> (js->clj theme :keywordize-keys true)
                       (update :colors merge {:primary "#3358d4"
                                              :primary25 "#d2deff"
                                              :primary50 "#abbdf9"
                                              :primary75 "#3e63dd"}))))
         :className "react-select-container"
         :classNamePrefix "react-select"
         :ref #(reset! container-ref %)
         :styles styles}]])))

;; Styles for the react-select "Fixed Options" pattern: fixed pills hide the
;; remove button and get a distinct neutral background. The style fns receive
;; (base, state) — react-select always passes both; the shared `styles` def
;; simply ignores the second argument.
(defn- managed-option-state?
  "True when a react-select style-fn state refers to an option tagged isManaged."
  [state]
  (boolean (some-> state (gobj/getValueByKeys "data" "isManaged"))))

;; Managed pills (e.g. the protection-profile attribute) follow the Figma
;; accent style: light blue chip with blue label/icon. They stay removable.
(def ^:private managed-options-styles
  (clj->js
   (merge (js->clj styles)
          {"multiValue" (fn [style state]
                          (let [base (into (js->clj style)
                                           {"padding" "4px"
                                            "borderRadius" "12px"
                                            "fontSize" "16px"
                                            "backgroundColor" "rgba(0, 0, 51, 0.06)"})]
                            (if (managed-option-state? state)
                              (clj->js (into base {"backgroundColor" "#edf2fe"}))
                              (clj->js base))))
           "multiValueLabel" (fn [style state]
                               (clj->js (into (js->clj style)
                                              {"color" (if (managed-option-state? state)
                                                         "#3a5bc7"
                                                         "#60646c")})))
           "multiValueRemove" (fn [style state]
                                (if (managed-option-state? state)
                                  (clj->js (into (js->clj style) {"color" "#3a5bc7"}))
                                  style))})))

(defn- format-option-with-managed-icon
  "Renders managed options (pills and menu entries) with an award icon;
  regular options render as plain labels."
  [opt _ctx]
  (if (gobj/get opt "isManaged")
    (r/as-element
     [:span {:class "flex items-center gap-1"}
      [:> Award {:size 12 :aria-hidden true}]
      (gobj/get opt "label")])
    (gobj/get opt "label")))

(defn creatable-select
  "Creatable multi-select. Extra optional props for managed options (e.g. the
  protection-profile attribute in the role creation wizard):

  - :managed-options — vector of {:value v :label l} available managed entries.
    They render with a distinct blue style + award icon, appear in the
    dropdown, and are removable like any pill.
  - :managed-value — vector of currently selected managed values.
  - :on-managed-change — (fn [managed-values]) called with the selected
    managed values whenever they change.

  Managed entries never reach the regular `on-change` — callers keep user
  values and managed values in separate state."
  [{:keys [default-value disabled? required? on-change on-create-option options label id name placeholder
           managed-options managed-value on-managed-change]}]
  (let [managed (mapv #(assoc % :isManaged true) managed-options)
        has-managed? (seq managed)
        managed-selected (filterv #(some #{(:value %)} (or managed-value [])) managed)
        value (if has-managed?
                (into managed-selected default-value)
                default-value)
        ;; Managed entries not currently selected stay available in the menu.
        options* (if has-managed?
                   (into (vec (remove #(some #{(:value %)} (or managed-value [])) managed))
                         options)
                   options)
        handle-change (if has-managed?
                        (fn [js-value _action-meta]
                          (let [values (js->clj js-value :keywordize-keys true)
                                managed-values (mapv :value (filter :isManaged values))
                                user-options (vec (remove :isManaged values))]
                            (when on-managed-change
                              (on-managed-change managed-values))
                            (on-change (clj->js user-options))))
                        on-change)]
    [:div {:class "mb-regular text-sm"}
     [:div {:class "flex items-center gap-2 mb-1"}
      (when label
        [form-label label])]
     [:> CreatableSelect
      (merge {:value value
              :id id
              :name name
              :isMulti true
              :isDisabled disabled?
              :required required?
              :onChange handle-change
              :options options*
              :isClearable false
              :theme (fn [theme]
                       (clj->js
                        (-> (js->clj theme :keywordize-keys true)
                            (update :colors merge {:primary "#3358d4"
                                                   :primary25 "#d2deff"
                                                   :primary50 "#abbdf9"
                                                   :primary75 "#3e63dd"}))))
              :menuPortalTarget (.-body js/document)
              :className "react-select-container"
              :classNamePrefix "react-select"
              :styles (if has-managed? managed-options-styles styles)}
             (when has-managed? {:formatOptionLabel format-option-with-managed-icon})
             (when placeholder {:placeholder placeholder})
             (when on-create-option {:onCreateOption on-create-option}))]]))

(defn text-input
  "Renders a text input that supports multiple values.

    Params:
    - value: A reagent atom that holds the current value of the text input.
    - input-value: A reagent atom that holds the current input value of the text input.
    - disabled?: A boolean indicating whether the text input is disabled.
    - required?: A boolean indicating whether the text input is required.
    - on-change: A function that is called when the value of the text input changes.
    - on-input-change: A function that is called when the input value of the text input changes.
    - label: A string that is used as the label of the text input.
    - id: A string that is used as the id of the text input.
    - name: A string that is used as the name of the text input.

    Behaviors:
    - When the 'Enter' or 'Tab' key is pressed, if there is a current input value, it is added to the value list and the input value is cleared.
    - The 'Enter' and 'Tab' key events are prevented from propagating to avoid unwanted side effects.
    - The text input is rendered using the CreatableSelect component from the react-select library.
    - The text input supports multiple values (isMulti is set to true).
    - The dropdown indicator of the text input is hidden (:components #js{:DropdownIndicator nil}).
    - The menu of the text input is always closed (:menuIsOpen false).
    - The text input can be cleared (:isClearable true).
    - The text input has specific class names for styling purposes (:className and :classNamePrefix)."

  [{:keys [value input-value disabled? required? on-change on-input-change label label-description id name]}]
  (let [handleKeyDown (fn [event]
                        (if input-value
                          (case (.-key event)
                            "Enter" (do
                                      (on-change (conj (js->clj value) {"label" input-value "value" input-value "id" (str (random-uuid))}))
                                      (on-input-change "")
                                      (.preventDefault event))
                            "Tab" (do
                                    (on-change (conj (js->clj value) {"label" input-value "value" input-value "id" (str (random-uuid))}))
                                    (on-input-change "")
                                    (.preventDefault event))
                            nil)
                          nil))]
    [:div {:class "text-sm"}
     [:div {:class "flex flex-col justify-center mb-1"}
      (when label
        [form-label label])
      (when label-description
        [:span {:class "text-xs text-gray-500"} label-description])]
     [:> CreatableSelect
      {:components #js{:DropdownIndicator nil}
       :value value
       :inputValue input-value
       :id id
       :name name
       :isMulti true
       :menuIsOpen false
       :isDisabled disabled?
       :required required?
       :getOptionValue (fn [option]
                         (or (gobj/get option "id")
                             (gobj/get option "value")))
       :onChange (fn [value]
                   (on-change (js->clj value)))
       :onInputChange on-input-change
       :onKeyDown handleKeyDown
       :theme (fn [theme]
                (clj->js
                 (-> (js->clj theme :keywordize-keys true)
                     (update :colors merge {:primary "#3358d4"
                                            :primary25 "#d2deff"
                                            :primary50 "#abbdf9"
                                            :primary75 "#3e63dd"}))))
       :isClearable true
       :className "react-select-container"
       :classNamePrefix "react-select"
       :styles styles}]]))

(defn single []
  (fn [{:keys [default-value disabled? helper-text required? clearable? searchble? on-change options label id name]}]
    [:div {:class "mb-regular text-sm"}

     (when (or label
               (not (cs/blank? helper-text)))
       [:div {:class "flex items-center mb-1 gap-2"}
        (when label
          [form-label label])
        (when (not (cs/blank? helper-text))
          [:> Tooltip {:content helper-text}
           [:> HelpCircle {:size 14}]])])

     [:> Select
      {:value default-value
       :id id
       :name name
       :isDisabled disabled?
       :required required?
       :onChange (fn [value]
                   (on-change value))
       :options options
       :theme (fn [theme]
                (clj->js
                 (-> (js->clj theme :keywordize-keys true)
                     (update :colors merge {:primary "#3358d4"
                                            :primary25 "#d2deff"
                                            :primary50 "#abbdf9"
                                            :primary75 "#3e63dd"}))))
       :isClearable clearable?
       :isSearchable searchble?
       :className "react-select-container"
       :classNamePrefix "react-select"
       :styles styles}]]))

(defn single-creatable-grouped
  "A single-select component that allows creating new options and supports grouped options.

   Props:
   - default-value: The currently selected value
   - disabled?: Whether the select is disabled
   - required?: Whether the select is required
   - on-change: Function called when selection changes
   - on-create-option: Function called when a new option is created
   - options: The options for the select, with grouping structure:
     [{label: 'Group1', options: [{value: 'val1', label: 'Label1'}, ...], ...}]
   - label: Label text to display above the select
   - id: HTML id attribute
   - name: HTML name attribute
   - placeholder: Placeholder text
   - format-create-label: (Optional) Function to format the 'Create option' text"
  [{:keys [default-value disabled? required?
           on-change on-create-option options
           label id name placeholder
           format-create-label]}]
  [:div {:class "text-sm"}
   [:div {:class "flex items-center gap-2"}
    (when label
      [form-label label])]
   [:> CreatableSelect
    {:value default-value
     :id id
     :name name
     :isMulti false
     :isDisabled disabled?
     :required required?
     :onChange (fn [value]
                 (on-change value))
     :onCreateOption (fn [input-value]
                       (when on-create-option
                         (on-create-option input-value)))
     :options options
     :placeholder (or placeholder "Select or create...")
     :formatCreateLabel (or format-create-label #(str "Create \"" % "\""))
     :menuPortalTarget (.-body js/document)
     :theme (fn [theme]
              (clj->js
               (-> (js->clj theme :keywordize-keys true)
                   (update :colors merge {:primary "#3358d4"
                                          :primary25 "#d2deff"
                                          :primary50 "#abbdf9"
                                          :primary75 "#3e63dd"}))))
     :isClearable true
     :isSearchable true
     :className "react-select-container"
     :classNamePrefix "react-select"
     :styles styles}]])

(defn- build-paginated-styles
  "Memoized style builder to avoid recreating styles on every render"
  []
  (clj->js (merge (js->clj styles)
                  {"menu" (fn [style]
                            (clj->js (merge (js->clj style)
                                            {"maxHeight" "300px"
                                             "overflow" "auto"})))})))

(def ^:private memoized-paginated-styles (memoize build-paginated-styles))

(defn- custom-menu-list
  "Custom MenuList component that integrates infinite scroll"
  [props children on-load-more has-more? loading?]
  (let [class-name (.-className props)
        inner-props (.-innerProps props)
        ;; Check if we have actual options to show
        options (.-options (.-selectProps props))
        has-options? (and options (> (.-length options) 0))]
    [:div (merge (js->clj inner-props :keywordize-keys true) {:className class-name})
     (if has-options?
       [infinite-scroll
        {:on-load-more on-load-more
         :has-more? has-more?
         :loading? loading?}
        children]
       children)]))

(defn paginated
  "A multiselect component with infinite scroll pagination for loading large option sets.

   Props:
   - default-value: The currently selected values (array)
   - disabled?: Whether the select is disabled
   - required?: Whether the select is required
   - on-change: Function called when selection changes
   - options: Current loaded options array
   - label: Label text to display above the select
   - id: HTML id attribute
   - name: HTML name attribute
   - on-load-more: Function called to load more options
   - has-more?: Boolean indicating if more options are available
   - loading?: Boolean indicating if options are currently loading
   - on-input-change: Function called when search input changes (for search functionality)
   - search-value: Current search input value
   - placeholder: Placeholder text for the select"
  [{:keys [default-value disabled? required? on-change options label id name
           on-load-more has-more? loading? on-input-change search-value placeholder]}]
  (let [container-ref (r/atom nil)
        paginated-styles (memoized-paginated-styles)

        menu-list-fn (fn [props]
                       (r/as-element
                        [custom-menu-list props (.-children props)
                         on-load-more has-more? loading?]))]

    [:div {:class "mb-regular text-sm"}
     [:div {:class "flex items-center mb-1"}
      (when label
        [form-label label])]
     [:> Select
      {:value default-value
       :id id
       :name name
       :isMulti true
       :isDisabled disabled?
       :isLoading loading?
       :required required?
       :onChange (fn [value]
                   (scroll-to-bottom @container-ref)
                   (on-change value))
       :onInputChange (fn [input-value action]
                        (when (and on-input-change
                                   (= (.-action action) "input-change"))
                          (on-input-change input-value)))
       :inputValue search-value
       :options options
       :filterOption false
       :components #js {:MenuList menu-list-fn}
       :isClearable false
       :onFocus #(scroll-to-bottom @container-ref)
       :placeholder (or placeholder "Select options...")
       :menuPortalTarget (.-body js/document)
       :theme (fn [theme]
                (clj->js
                 (-> (js->clj theme :keywordize-keys true)
                     (update :colors merge {:primary "#3358d4"
                                            :primary25 "#d2deff"
                                            :primary50 "#abbdf9"
                                            :primary75 "#3e63dd"}))))
       :className "react-select-container"
       :classNamePrefix "react-select"
       :ref #(reset! container-ref %)
       :styles paginated-styles}]]))
